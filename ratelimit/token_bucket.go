package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc"
)

// 定义错误类型
var (
	ErrLimitExceeded = errors.New("rate limit exceeded")
)

// TokenBucketOptions 令牌桶配置选项
type TokenBucketOptions struct {
	// 每秒生成的令牌数
	Rate float64
	// 桶的容量
	BucketSize float64
}

// TokenBucket 令牌桶实现
type TokenBucket struct {
	rate       float64    // 每秒填充的令牌数
	bucketSize float64    // 桶的大小
	tokens     float64    // 当前令牌数量
	lastRefill time.Time  // 上次填充时间
	mutex      sync.Mutex // 互斥锁
}

// NewTokenBucket 创建一个新的令牌桶
func NewTokenBucket(opts TokenBucketOptions) *TokenBucket {
	return &TokenBucket{
		rate:       opts.Rate,
		bucketSize: opts.BucketSize,
		tokens:     opts.BucketSize, // 初始时桶是满的
		lastRefill: time.Now(),
	}
}

// refill 填充令牌
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	// 计算需要添加的令牌数
	newTokens := elapsed * tb.rate

	// 更新令牌数，但不超过桶容量
	tb.tokens += newTokens
	if tb.tokens > tb.bucketSize {
		tb.tokens = tb.bucketSize
	}
}

// Allow 检查是否允许通过一个请求
func (tb *TokenBucket) Allow() bool {
	tb.mutex.Lock()
	defer tb.mutex.Unlock()

	tb.refill()

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

// UpdateRate 更新令牌桶的速率
func (tb *TokenBucket) UpdateRate(newRate float64) {
	tb.mutex.Lock()
	defer tb.mutex.Unlock()

	tb.refill() // 先根据旧速率更新令牌
	tb.rate = newRate
}

// TokenBucketLimiterOptions defines the type for configuration options on the TokenBucketLimiter.
type TokenBucketLimiterOptions func(l *TokenBucketLimiter)

// TokenBucketLimiter controls access to a resource by enforcing a rate limit using a token bucket strategy.
type TokenBucketLimiter struct {
	tokens   chan struct{}  // Channel used to represent tokens that control rate limiting.
	close    chan struct{}  // Channel used to signal shutdown.
	onReject rejectStrategy // Strategy to be used when a request is rejected.
	mutex    sync.Mutex     // Mutex to protect shared state.
}

// NewTokenBucketLimiter constructs a new TokenBucketLimiter with the given capacity and token refill interval.
func NewTokenBucketLimiter(capacity int, interval time.Duration) *TokenBucketLimiter {
	tokensCh := make(chan struct{}, capacity)
	closeCh := make(chan struct{})

	producer := time.NewTicker(interval)
	go func() {
		defer producer.Stop()
		for {
			select {
			case <-producer.C: // Refill token.
				select {
				case tokensCh <- struct{}{}: // Attempt to add a token.
				default: // Skip if channel is already full.
				}
			case <-closeCh: // Stop the goroutine if close signal is received.
				return
			}
		}
	}()

	return &TokenBucketLimiter{
		tokens:   tokensCh,
		close:    closeCh,
		onReject: defaultRejectStrategy, // Set default reject strategy on new limiter.
	}
}

// TokenBucketMarkFailed provides a configuration option to use the markFailedStrategy when a request is rejected.
func TokenBucketMarkFailed() TokenBucketLimiterOptions {
	return func(l *TokenBucketLimiter) {
		l.mutex.Lock()
		defer l.mutex.Unlock()
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that enforces the token bucket rate limiting.
func (l *TokenBucketLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		select {
		case <-l.tokens: // Allow the request to be handled if a token is available.
			return handler(ctx, req)
		case <-ctx.Done(): // Reject the request if the provided context is already done.
			return l.onReject(ctx, req, info, handler)
		case <-l.close: // Reject the request if the limiter is being closed.
			return nil, ErrLimitExceeded // 使用本地定义的错误
		}
	}
}

// Close cleanly stops the token refill and shuts down the limiter.
func (l *TokenBucketLimiter) Close() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.close != nil {
		close(l.close)
		l.close = nil // Prevent closing the channel more than once and causing a panic.
	}

	return nil
}

// 拒绝策略类型定义
type rejectStrategy func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error)

// 默认拒绝策略：返回资源耗尽错误
var defaultRejectStrategy rejectStrategy = func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return nil, ErrLimitExceeded // 使用本地定义的错误
}

// 标记失败策略：在错误上下文中标记请求失败了
var markFailedStrategy rejectStrategy = func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	// 处理请求但标记为受限
	ctx = context.WithValue(ctx, contextKeyRateLimited, true)
	return handler(ctx, req)
}

// 上下文键
type contextKey int

const (
	contextKeyRateLimited contextKey = iota
)

// LimiterStats 限流器统计信息
type LimiterStats struct {
	AllowedRequests  uint64
	RejectedRequests uint64
	CurrentRate      float64
}
