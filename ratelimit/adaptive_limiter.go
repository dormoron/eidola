package ratelimit

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// AdaptiveLimiterOptions 自适应限流器的配置选项
type AdaptiveLimiterOptions struct {
	// 初始QPS限制
	InitialRate float64
	// 最小QPS限制
	MinRate float64
	// 最大QPS限制
	MaxRate float64
	// CPU使用率阈值 (0-1)，超过此值将降低限制
	HighLoadThreshold float64
	// CPU使用率阈值 (0-1)，低于此值将提高限制
	LowLoadThreshold float64
	// 降低系数，当负载过高时QPS限制的降低系数
	DecreaseFactorOnHeavyLoad float64
	// 增加系数，当负载过低时QPS限制的增加系数
	IncreaseFactorOnLightLoad float64
	// 响应时间阈值，超过此值将视为系统负载较高
	ResponseTimeThreshold time.Duration
	// 响应时间采样窗口大小
	ResponseTimeSampleSize int
	// 采样间隔，多久检查一次系统负载
	SamplingInterval time.Duration
	// 是否在过载时直接拒绝请求而不是延迟
	RejectOnOverload bool
	// 采样窗口大小
	WindowSize int
}

// DefaultAdaptiveLimiterOptions 返回默认的自适应限流器配置
func DefaultAdaptiveLimiterOptions() AdaptiveLimiterOptions {
	return AdaptiveLimiterOptions{
		InitialRate:               100,
		MinRate:                   10,
		MaxRate:                   1000,
		HighLoadThreshold:         0.75, // 75% CPU使用率视为高负载
		LowLoadThreshold:          0.50, // 50% CPU使用率视为低负载
		DecreaseFactorOnHeavyLoad: 0.9,  // 高负载时降低到当前的90%
		IncreaseFactorOnLightLoad: 1.1,  // 低负载时增加到当前的110%
		ResponseTimeThreshold:     100 * time.Millisecond,
		ResponseTimeSampleSize:    100,
		SamplingInterval:          5 * time.Second,
		RejectOnOverload:          true,
		WindowSize:                500,
	}
}

// AdaptiveLimiter 自适应限流器，根据系统负载动态调整请求速率
type AdaptiveLimiter struct {
	opts               AdaptiveLimiterOptions
	currentRate        float64      // 当前限流速率
	tokenBucket        *TokenBucket // 内部使用令牌桶实现基础限流
	responseTimes      []time.Duration
	responseTimeIndex  int
	responseTimeMutex  sync.Mutex
	lastSampleTime     time.Time
	consecutiveRejects int32
	stats              LimiterStats
	statsMu            sync.RWMutex
	stopCh             chan struct{}
	wg                 sync.WaitGroup
}

// NewAdaptiveLimiter 创建新的自适应限流器
func NewAdaptiveLimiter(opts AdaptiveLimiterOptions) *AdaptiveLimiter {
	if opts.WindowSize <= 0 {
		opts.WindowSize = 500
	}

	if opts.ResponseTimeSampleSize <= 0 {
		opts.ResponseTimeSampleSize = 100
	}

	limiter := &AdaptiveLimiter{
		opts:              opts,
		responseTimes:     make([]time.Duration, opts.ResponseTimeSampleSize),
		responseTimeIndex: 0,
		lastSampleTime:    time.Now(),
		stopCh:            make(chan struct{}),
		stats: LimiterStats{
			AllowedRequests:  0,
			RejectedRequests: 0,
			CurrentRate:      opts.InitialRate,
		},
	}

	limiter.currentRate = opts.InitialRate
	limiter.tokenBucket = NewTokenBucket(TokenBucketOptions{
		Rate:       opts.InitialRate,
		BucketSize: float64(opts.InitialRate) / 10.0, // 桶容量为QPS的十分之一，允许短时间突发
	})

	// 启动自适应控制协程
	limiter.wg.Add(1)
	go limiter.adaptiveControl()

	return limiter
}

// Allow 检查请求是否可以通过
func (l *AdaptiveLimiter) Allow() bool {
	allowed := l.tokenBucket.Allow()

	l.statsMu.Lock()
	if allowed {
		l.stats.AllowedRequests++
		l.stats.CurrentRate = l.currentRate
	} else {
		l.stats.RejectedRequests++
		atomic.AddInt32(&l.consecutiveRejects, 1)
	}
	l.statsMu.Unlock()

	return allowed
}

// AllowWithContext 带上下文的限流检查
func (l *AdaptiveLimiter) AllowWithContext(ctx context.Context) error {
	start := time.Now()

	// 检查是否允许请求通过
	if !l.Allow() {
		if l.opts.RejectOnOverload {
			return ErrLimitExceeded
		}

		// 非拒绝模式，尝试等待令牌
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second): // 最多等待1秒
			if !l.Allow() {
				return ErrLimitExceeded
			}
		}
	}

	// 记录请求开始时间，用于后续计算响应时间
	ctx = context.WithValue(ctx, ctxKeyRequestStartTime, start)
	return nil
}

// RecordMetrics 记录请求执行的指标
func (l *AdaptiveLimiter) RecordMetrics(ctx context.Context) {
	startTimeVal := ctx.Value(ctxKeyRequestStartTime)
	if startTimeVal == nil {
		return
	}

	startTime, ok := startTimeVal.(time.Time)
	if !ok {
		return
	}

	// 计算请求响应时间
	responseTime := time.Since(startTime)

	// 更新响应时间样本
	l.responseTimeMutex.Lock()
	l.responseTimes[l.responseTimeIndex] = responseTime
	l.responseTimeIndex = (l.responseTimeIndex + 1) % len(l.responseTimes)
	l.responseTimeMutex.Unlock()
}

// adaptiveControl 自适应控制协程，定期根据系统负载调整限流速率
func (l *AdaptiveLimiter) adaptiveControl() {
	defer l.wg.Done()

	ticker := time.NewTicker(l.opts.SamplingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.adjustRate()
		}
	}
}

// adjustRate 根据系统负载和响应时间调整限流速率
func (l *AdaptiveLimiter) adjustRate() {
	// 获取当前CPU使用率
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 计算平均响应时间
	var totalTime time.Duration
	var count int

	l.responseTimeMutex.Lock()
	for _, t := range l.responseTimes {
		if t > 0 {
			totalTime += t
			count++
		}
	}
	l.responseTimeMutex.Unlock()

	var avgResponseTime time.Duration
	if count > 0 {
		avgResponseTime = totalTime / time.Duration(count)
	}

	// 获取系统CPU负载
	cpuUsage := getCPUUsage()

	// 根据负载和响应时间调整速率
	currentRate := l.currentRate
	newRate := currentRate

	// 基于CPU使用率调整
	if cpuUsage > l.opts.HighLoadThreshold {
		// 高负载情况，降低速率
		newRate = currentRate * l.opts.DecreaseFactorOnHeavyLoad
	} else if cpuUsage < l.opts.LowLoadThreshold &&
		avgResponseTime < l.opts.ResponseTimeThreshold {
		// 低负载情况，增加速率
		newRate = currentRate * l.opts.IncreaseFactorOnLightLoad
	}

	// 基于响应时间进行额外调整
	if avgResponseTime > l.opts.ResponseTimeThreshold {
		// 响应时间过长，进一步降低速率
		newRate = newRate * 0.95
	}

	// 确保速率在配置的范围内
	if newRate < l.opts.MinRate {
		newRate = l.opts.MinRate
	} else if newRate > l.opts.MaxRate {
		newRate = l.opts.MaxRate
	}

	// 应用新速率
	l.currentRate = newRate
	l.tokenBucket.UpdateRate(newRate)

	// 重置连续拒绝计数
	atomic.StoreInt32(&l.consecutiveRejects, 0)
}

// GetStats 获取限流器统计信息
func (l *AdaptiveLimiter) GetStats() LimiterStats {
	l.statsMu.RLock()
	defer l.statsMu.RUnlock()

	stats := l.stats
	stats.CurrentRate = l.currentRate
	return stats
}

// Close 关闭限流器并释放资源
func (l *AdaptiveLimiter) Close() error {
	close(l.stopCh)
	l.wg.Wait()
	return nil
}

// 上下文键
type ctxKey int

const ctxKeyRequestStartTime ctxKey = iota

// getCPUUsage 获取当前系统CPU使用率
func getCPUUsage() float64 {
	// 简单实现，实际生产中应使用系统特定的方法获取更准确的CPU使用率
	var cpuUsage float64 = 0.5 // 默认假设50%的负载

	// 这里只是占位符，实际实现应该使用系统API获取当前CPU使用率
	// 在Linux上可以解析/proc/stat，在Windows上可以使用WMI或性能计数器
	// 某些环境中可以使用shirou/gopsutil等库获取系统信息

	return cpuUsage
}
