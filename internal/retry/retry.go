package retry

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Strategy 定义重试策略类型
type Strategy uint8

const (
	// FixedDelay 固定延迟重试
	FixedDelay Strategy = iota
	// ExponentialBackoff 指数退避重试
	ExponentialBackoff
	// RandomizedBackoff 随机抖动重试
	RandomizedBackoff
)

// Policy 定义重试策略的配置
type Policy struct {
	// 是否启用重试
	Enabled bool
	// 最大重试次数
	MaxAttempts uint
	// 基础延迟时间
	BaseDelay time.Duration
	// 最大延迟时间
	MaxDelay time.Duration
	// 重试策略
	Strategy Strategy
	// 抖动因子 (0.0-1.0)，用于随机化退避时间
	JitterFactor float64
	// 重试状态码
	RetryableCodes []codes.Code
	// 重试超时因子 (>1.0)，用于增加每次重试的超时时间
	TimeoutFactor float64
	// 最大重试超时时间
	MaxTimeout time.Duration
}

// DefaultPolicy 默认重试策略
var DefaultPolicy = Policy{
	Enabled:        true,
	MaxAttempts:    3,
	BaseDelay:      100 * time.Millisecond,
	MaxDelay:       5 * time.Second,
	Strategy:       ExponentialBackoff,
	JitterFactor:   0.2,
	RetryableCodes: []codes.Code{codes.Unavailable, codes.ResourceExhausted, codes.Aborted, codes.Internal},
	TimeoutFactor:  1.5,
	MaxTimeout:     30 * time.Second,
}

// Budget 定义重试预算
type Budget struct {
	// 最大并发重试次数
	MaxConcurrentRetries int32
	// 当前并发重试次数
	currentRetries int32
	// 最大重试比例 (0.0-1.0)，相对于总请求数
	MaxRetryRatio float64
	// 统计数据
	totalRequests   int64
	retriedRequests int64
	mutex           sync.RWMutex
}

// DefaultBudget 默认重试预算
var DefaultBudget = Budget{
	MaxConcurrentRetries: 100,
	MaxRetryRatio:        0.1, // 最多10%的请求可以重试
}

// CanRetry 检查是否可以在预算内重试
func (b *Budget) CanRetry() bool {
	// 检查并发重试数
	if atomic.LoadInt32(&b.currentRetries) >= b.MaxConcurrentRetries {
		return false
	}

	// 检查重试比例
	b.mutex.RLock()
	totalReqs := b.totalRequests
	retriedReqs := b.retriedRequests
	b.mutex.RUnlock()

	if totalReqs > 0 && float64(retriedReqs)/float64(totalReqs) >= b.MaxRetryRatio {
		return false
	}

	return true
}

// RegisterRequest 注册一个新请求
func (b *Budget) RegisterRequest() {
	b.mutex.Lock()
	b.totalRequests++
	b.mutex.Unlock()
}

// RegisterRetry 注册一个重试
func (b *Budget) RegisterRetry() {
	atomic.AddInt32(&b.currentRetries, 1)
	b.mutex.Lock()
	b.retriedRequests++
	b.mutex.Unlock()
}

// ReleaseRetry 释放一个重试
func (b *Budget) ReleaseRetry() {
	atomic.AddInt32(&b.currentRetries, -1)
}

// RetryInterceptor 实现重试功能的拦截器
type RetryInterceptor struct {
	policy Policy
	budget *Budget
	rand   *rand.Rand
	mu     sync.Mutex // 保护随机数生成器
}

// NewRetryInterceptor 创建一个新的重试拦截器
func NewRetryInterceptor(policy Policy, budget *Budget) *RetryInterceptor {
	if budget == nil {
		budget = &DefaultBudget
	}

	src := rand.NewSource(time.Now().UnixNano())

	return &RetryInterceptor{
		policy: policy,
		budget: budget,
		rand:   rand.New(src),
	}
}

// UnaryClientInterceptor 返回一个一元RPC客户端拦截器
func (r *RetryInterceptor) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if !r.policy.Enabled {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		// 注册请求
		r.budget.RegisterRequest()

		var lastErr error
		for attempt := uint(0); attempt <= r.policy.MaxAttempts; attempt++ {
			if attempt > 0 {
				// 判断是否可以重试
				if !r.budget.CanRetry() {
					return lastErr
				}

				// 计算重试延迟
				delay := r.calculateDelay(attempt)

				// 使用context检查取消
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
					// 继续重试
				}

				// 注册重试
				r.budget.RegisterRetry()
				defer r.budget.ReleaseRetry()

				// 为重试创建新的上下文，适当增加超时
				if deadline, ok := ctx.Deadline(); ok {
					var timeout time.Duration
					now := time.Now()
					if deadline.After(now) {
						originalTimeout := deadline.Sub(now)
						timeout = time.Duration(float64(originalTimeout) * r.policy.TimeoutFactor)
						if timeout > r.policy.MaxTimeout {
							timeout = r.policy.MaxTimeout
						}
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, timeout)
						defer cancel()
					}
				}
			}

			// 调用原始方法
			err := invoker(ctx, method, req, reply, cc, opts...)
			if err == nil {
				return nil
			}

			// 记录最后一个错误
			lastErr = err

			// 检查是否是可重试的错误
			if !r.isRetryable(err) || attempt == r.policy.MaxAttempts {
				return err
			}
		}

		return lastErr
	}
}

// isRetryable 检查错误是否可重试
func (r *RetryInterceptor) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// 获取gRPC状态码
	st, ok := status.FromError(err)
	if !ok {
		return false
	}

	// 检查是否在可重试状态码列表中
	for _, code := range r.policy.RetryableCodes {
		if st.Code() == code {
			return true
		}
	}

	return false
}

// calculateDelay 计算重试延迟
func (r *RetryInterceptor) calculateDelay(attempt uint) time.Duration {
	var delay time.Duration

	switch r.policy.Strategy {
	case FixedDelay:
		delay = r.policy.BaseDelay
	case ExponentialBackoff:
		delay = time.Duration(float64(r.policy.BaseDelay) * math.Pow(2, float64(attempt-1)))
	case RandomizedBackoff:
		baseDelay := time.Duration(float64(r.policy.BaseDelay) * math.Pow(2, float64(attempt-1)))
		r.mu.Lock()
		jitter := time.Duration(r.rand.Float64() * r.policy.JitterFactor * float64(baseDelay))
		r.mu.Unlock()
		delay = baseDelay + jitter
	}

	// 确保不超过最大延迟
	if delay > r.policy.MaxDelay {
		delay = r.policy.MaxDelay
	}

	return delay
}

// StreamClientInterceptor 返回一个流式RPC客户端拦截器
func (r *RetryInterceptor) StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if !r.policy.Enabled {
			return streamer(ctx, desc, cc, method, opts...)
		}

		// 注册请求
		r.budget.RegisterRequest()

		var lastErr error
		for attempt := uint(0); attempt <= r.policy.MaxAttempts; attempt++ {
			if attempt > 0 {
				// 判断是否可以重试
				if !r.budget.CanRetry() {
					return nil, lastErr
				}

				// 计算重试延迟
				delay := r.calculateDelay(attempt)

				// 使用context检查取消
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
					// 继续重试
				}

				// 注册重试
				r.budget.RegisterRetry()
				defer r.budget.ReleaseRetry()
			}

			// 创建流
			stream, err := streamer(ctx, desc, cc, method, opts...)
			if err == nil {
				return stream, nil
			}

			// 记录最后一个错误
			lastErr = err

			// 检查是否是可重试的错误
			if !r.isRetryable(err) || attempt == r.policy.MaxAttempts {
				return nil, err
			}
		}

		return nil, lastErr
	}
}

// WithRetryPolicy 返回一个CallOption，用于设置单个调用的重试策略
func WithRetryPolicy(policy Policy) grpc.CallOption {
	return grpc.EmptyCallOption{}
}
