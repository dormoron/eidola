package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

// BackoffType 定义退避类型
type BackoffType int

// RetryableFunc 是可重试的函数类型
type RetryableFunc func() error

// RetryableFuncWithContext 是带上下文的可重试函数类型
type RetryableFuncWithContext func(context.Context) error

// BackoffStrategy 退避策略接口
type BackoffStrategy interface {
	// NextBackoff 计算下一次退避时间
	NextBackoff(attempt int) time.Duration
	// Reset 重置退避状态
	Reset()
}

// ExponentialBackoffStrategy 指数退避策略实现
type ExponentialBackoffStrategy struct {
	// 初始退避时间
	initialBackoff time.Duration
	// 最大退避时间
	maxBackoff time.Duration
	// 退避系数
	factor float64
}

// NewExponentialBackoffStrategy 创建一个新的指数退避策略
func NewExponentialBackoffStrategy(initialBackoff, maxBackoff time.Duration, factor float64) *ExponentialBackoffStrategy {
	if factor <= 1.0 {
		factor = 2.0 // 默认使用2作为系数
	}
	return &ExponentialBackoffStrategy{
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
		factor:         factor,
	}
}

// NextBackoff 计算下一次指数退避时间
func (s *ExponentialBackoffStrategy) NextBackoff(attempt int) time.Duration {
	backoff := float64(s.initialBackoff) * math.Pow(s.factor, float64(attempt))
	if backoff > float64(s.maxBackoff) {
		backoff = float64(s.maxBackoff)
	}
	return time.Duration(backoff)
}

// Reset 重置指数退避状态
func (s *ExponentialBackoffStrategy) Reset() {
	// 指数退避策略不需要状态重置
}

// JitteredBackoffStrategy 带抖动的退避策略实现
type JitteredBackoffStrategy struct {
	// 基础退避策略
	base BackoffStrategy
	// 抖动率 (0.0-1.0)
	jitterFactor float64
	// 随机数生成器
	rng *rand.Rand
	// 互斥锁保护随机数生成器
	mu sync.Mutex
}

// NewJitteredBackoffStrategy 创建一个新的带抖动的退避策略
func NewJitteredBackoffStrategy(base BackoffStrategy, jitterFactor float64) *JitteredBackoffStrategy {
	if jitterFactor < 0.0 {
		jitterFactor = 0.0
	}
	if jitterFactor > 1.0 {
		jitterFactor = 1.0
	}

	return &JitteredBackoffStrategy{
		base:         base,
		jitterFactor: jitterFactor,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextBackoff 计算下一次带抖动的退避时间
func (s *JitteredBackoffStrategy) NextBackoff(attempt int) time.Duration {
	baseBackoff := s.base.NextBackoff(attempt)

	s.mu.Lock()
	defer s.mu.Unlock()

	jitter := float64(baseBackoff) * s.jitterFactor * s.rng.Float64()
	return baseBackoff + time.Duration(jitter)
}

// Reset 重置带抖动的退避状态
func (s *JitteredBackoffStrategy) Reset() {
	s.base.Reset()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

// AdaptiveBackoffStrategy 自适应退避策略实现
type AdaptiveBackoffStrategy struct {
	// 初始退避时间
	initialBackoff time.Duration
	// 最大退避时间
	maxBackoff time.Duration
	// 当前平均延迟
	avgLatency time.Duration
	// 平滑因子 (0.0-1.0)
	smoothingFactor float64
	// 成功计数
	successCount int
	// 失败计数
	failureCount int
	// 最后一次成功时间
	lastSuccess time.Time
	// 互斥锁保护状态
	mu sync.RWMutex
}

// NewAdaptiveBackoffStrategy 创建一个新的自适应退避策略
func NewAdaptiveBackoffStrategy(initialBackoff, maxBackoff time.Duration, smoothingFactor float64) *AdaptiveBackoffStrategy {
	if smoothingFactor < 0.0 {
		smoothingFactor = 0.0
	}
	if smoothingFactor > 1.0 {
		smoothingFactor = 1.0
	}

	return &AdaptiveBackoffStrategy{
		initialBackoff:  initialBackoff,
		maxBackoff:      maxBackoff,
		avgLatency:      initialBackoff,
		smoothingFactor: smoothingFactor,
		lastSuccess:     time.Now(),
	}
}

// NextBackoff 计算下一次自适应退避时间
func (s *AdaptiveBackoffStrategy) NextBackoff(attempt int) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 如果有很多连续失败，增加退避时间
	failureFactor := math.Min(float64(s.failureCount), 10.0) / 10.0
	adaptiveBackoff := float64(s.avgLatency) * (1.0 + failureFactor)

	// 应用指数增长
	backoff := adaptiveBackoff * math.Pow(2.0, float64(attempt))

	// 确保在范围内
	if backoff < float64(s.initialBackoff) {
		backoff = float64(s.initialBackoff)
	}
	if backoff > float64(s.maxBackoff) {
		backoff = float64(s.maxBackoff)
	}

	return time.Duration(backoff)
}

// Reset 重置自适应退避状态
func (s *AdaptiveBackoffStrategy) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failureCount = 0
	s.successCount = 0
	s.avgLatency = s.initialBackoff
	s.lastSuccess = time.Now()
}

// RecordSuccess 记录成功请求及其延迟
func (s *AdaptiveBackoffStrategy) RecordSuccess(latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.successCount++
	s.failureCount = 0 // 重置失败计数
	s.lastSuccess = time.Now()

	// 更新平均延迟，使用指数加权移动平均
	if s.avgLatency == 0 {
		s.avgLatency = latency
	} else {
		s.avgLatency = time.Duration(float64(s.avgLatency)*(1-s.smoothingFactor) + float64(latency)*s.smoothingFactor)
	}
}

// RecordFailure 记录失败请求
func (s *AdaptiveBackoffStrategy) RecordFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failureCount++
	s.successCount = 0 // 重置成功计数

	// 如果失败，稍微增加平均延迟
	s.avgLatency = time.Duration(float64(s.avgLatency) * 1.1)
	if s.avgLatency > s.maxBackoff {
		s.avgLatency = s.maxBackoff
	}
}

// Retrier 重试器
type Retrier struct {
	// 最大重试次数
	maxAttempts int
	// 退避策略
	backoffStrategy BackoffStrategy
	// 重试条件函数，返回true表示应该重试
	retryCondition func(error) bool
	// 使用上下文
	useContext bool
}

// RetryOption 配置Retrier的选项
type RetryOption func(*Retrier)

// WithMaxAttempts 设置最大重试次数
func WithMaxAttempts(maxAttempts int) RetryOption {
	return func(r *Retrier) {
		r.maxAttempts = maxAttempts
	}
}

// WithBackoffStrategy 设置退避策略
func WithBackoffStrategy(strategy BackoffStrategy) RetryOption {
	return func(r *Retrier) {
		r.backoffStrategy = strategy
	}
}

// WithRetryCondition 设置重试条件
func WithRetryCondition(condition func(error) bool) RetryOption {
	return func(r *Retrier) {
		r.retryCondition = condition
	}
}

// WithContext 设置使用上下文
func WithContext(useContext bool) RetryOption {
	return func(r *Retrier) {
		r.useContext = useContext
	}
}

// NewRetrier 创建一个新的重试器
func NewRetrier(opts ...RetryOption) *Retrier {
	// 默认配置
	r := &Retrier{
		maxAttempts:     3,
		backoffStrategy: NewJitteredBackoffStrategy(NewExponentialBackoffStrategy(100*time.Millisecond, 10*time.Second, 2.0), 0.2),
		retryCondition:  defaultRetryCondition,
		useContext:      true,
	}

	// 应用选项
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// defaultRetryCondition 默认重试条件
func defaultRetryCondition(err error) bool {
	if err == nil {
		return false
	}

	// 可以添加更多特定错误检查
	return true
}

// Do 执行带重试的操作
func (r *Retrier) Do(fn RetryableFunc) error {
	var err error
	attempt := 0

	for {
		err = fn()
		if err == nil {
			return nil // 成功完成
		}

		// 检查是否应该重试
		if !r.retryCondition(err) {
			return err // 不可重试的错误
		}

		attempt++
		if attempt >= r.maxAttempts {
			return err // 达到最大重试次数
		}

		// 计算退避时间并等待
		backoff := r.backoffStrategy.NextBackoff(attempt)
		time.Sleep(backoff)
	}
}

// DoWithContext 执行带上下文的重试操作
func (r *Retrier) DoWithContext(ctx context.Context, fn RetryableFuncWithContext) error {
	var err error
	attempt := 0

	for {
		// 检查上下文是否已取消
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err = fn(ctx)
		if err == nil {
			return nil // 成功完成
		}

		// 检查是否应该重试
		if !r.retryCondition(err) {
			return err // 不可重试的错误
		}

		attempt++
		if attempt >= r.maxAttempts {
			return err // 达到最大重试次数
		}

		// 计算退避时间并等待，同时考虑上下文取消
		backoff := r.backoffStrategy.NextBackoff(attempt)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// 继续重试
		}
	}
}

// IsRetryableError 检查错误是否可重试
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// 检查上下文取消或超时
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// 可以检查更多特定错误类型
	return true
}

// RetryWithAdaptiveBackoff 使用自适应退避重试函数
func RetryWithAdaptiveBackoff(ctx context.Context, fn RetryableFuncWithContext, maxAttempts int) error {
	// 创建自适应退避策略
	strategy := NewAdaptiveBackoffStrategy(100*time.Millisecond, 10*time.Second, 0.3)

	// 创建重试器
	retrier := NewRetrier(
		WithMaxAttempts(maxAttempts),
		WithBackoffStrategy(strategy),
		WithRetryCondition(IsRetryableError),
	)

	// 包装函数以记录成功和失败
	wrappedFn := func(ctx context.Context) error {
		startTime := time.Now()
		err := fn(ctx)
		latency := time.Since(startTime)

		if err == nil {
			strategy.RecordSuccess(latency)
		} else if IsRetryableError(err) {
			strategy.RecordFailure()
		}

		return err
	}

	return retrier.DoWithContext(ctx, wrappedFn)
}

// ListenableRetrier 可监听进度的重试器
type ListenableRetrier struct {
	*Retrier
	// 重试前回调
	beforeRetry func(attempt int, err error)
	// 重试后回调
	afterRetry func(attempt int, err error)
}

// NewListenableRetrier 创建一个可监听进度的重试器
func NewListenableRetrier(
	beforeRetry, afterRetry func(int, error),
	opts ...RetryOption,
) *ListenableRetrier {
	return &ListenableRetrier{
		Retrier:     NewRetrier(opts...),
		beforeRetry: beforeRetry,
		afterRetry:  afterRetry,
	}
}

// Do 执行带监听的重试操作
func (r *ListenableRetrier) Do(fn RetryableFunc) error {
	var err error
	attempt := 0

	for {
		if attempt > 0 && r.beforeRetry != nil {
			r.beforeRetry(attempt, err)
		}

		err = fn()

		if err == nil {
			return nil // 成功完成
		}

		if r.afterRetry != nil {
			r.afterRetry(attempt, err)
		}

		// 检查是否应该重试
		if !r.retryCondition(err) {
			return err // 不可重试的错误
		}

		attempt++
		if attempt >= r.maxAttempts {
			return err // 达到最大重试次数
		}

		// 计算退避时间并等待
		backoff := r.backoffStrategy.NextBackoff(attempt)
		time.Sleep(backoff)
	}
}
