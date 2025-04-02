package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/dormoron/eidola/internal/errs"
)

// AdaptiveOptions 自适应断路器配置选项
type AdaptiveOptions struct {
	// 基础配置继承自普通断路器
	BaseOptions Options

	// 自适应相关配置
	// 最小失败阈值
	MinThreshold uint32
	// 最大失败阈值
	MaxThreshold uint32
	// 最小超时时间
	MinTimeout time.Duration
	// 最大超时时间
	MaxTimeout time.Duration
	// 检测周期，多久检查一次系统状态并调整参数
	AdaptInterval time.Duration
	// 成功率提高时阈值降低的速率 (0-1)
	DecreaseThresholdRate float64
	// 成功率降低时阈值提高的速率 (0-1)
	IncreaseThresholdRate float64
	// 响应时间变化对参数调整的影响系数 (0-1)
	ResponseTimeImpact float64
	// 错误模式分析启用
	EnableErrorPatternAnalysis bool
	// 降级策略函数，返回在熔断时应该使用的降级处理逻辑
	FallbackHandler func(interface{}) (interface{}, error)
	// 监控回调函数，用于上报断路器状态变化
	OnStateChangeCallback func(name string, from, to State, metrics AdaptiveMetrics)
}

// DefaultAdaptiveOptions 返回默认的自适应断路器配置
func DefaultAdaptiveOptions() AdaptiveOptions {
	return AdaptiveOptions{
		BaseOptions:                DefaultOptions(),
		MinThreshold:               3,
		MaxThreshold:               50,
		MinTimeout:                 1 * time.Second,
		MaxTimeout:                 30 * time.Second,
		AdaptInterval:              10 * time.Second,
		DecreaseThresholdRate:      0.9, // 成功率提高时，阈值降低到原来的90%
		IncreaseThresholdRate:      1.2, // 成功率降低时，阈值提高到原来的120%
		ResponseTimeImpact:         0.3, // 响应时间对参数调整的影响权重为30%
		EnableErrorPatternAnalysis: true,
		FallbackHandler:            nil,
		OnStateChangeCallback:      nil,
	}
}

// ErrorPattern 错误模式
type ErrorPattern struct {
	// 错误类别
	Category errs.ErrorCategory
	// 错误代码
	Code string
	// 出现次数
	Count int
	// 第一次出现时间
	FirstSeen time.Time
	// 最后一次出现时间
	LastSeen time.Time
	// 建议的恢复策略
	SuggestedStrategy errs.ErrorRecoveryStrategy
}

// AdaptiveMetrics 自适应断路器的度量指标
type AdaptiveMetrics struct {
	// 当前状态
	CurrentState State
	// 当前失败阈值
	CurrentThreshold uint32
	// 当前超时时间
	CurrentTimeout time.Duration
	// 连续成功次数
	ConsecutiveSuccesses uint32
	// 连续失败次数
	ConsecutiveFailures uint32
	// 当前失败率 (0-1)
	FailureRate float64
	// 5分钟内的请求总数
	RequestCount5m uint32
	// 5分钟内的失败总数
	FailureCount5m uint32
	// 平均响应时间
	AverageResponseTime time.Duration
	// 错误模式统计
	ErrorPatterns []ErrorPattern
	// 上次调整时间
	LastAdaptTime time.Time
	// 从创建以来的总请求数
	TotalRequests uint64
	// 从创建以来的总失败数
	TotalFailures uint64
	// 当前半开状态下允许的请求数
	HalfOpenAllowed uint32
}

// AdaptiveCircuitBreaker 自适应断路器实现
type AdaptiveCircuitBreaker struct {
	*CircuitBreaker
	opts                AdaptiveOptions
	responseTimeSamples []time.Duration
	sampleIndex         int
	sampleMu            sync.Mutex
	errorPatterns       map[string]*ErrorPattern
	errorPatternMu      sync.RWMutex
	metrics             AdaptiveMetrics
	metricsMu           sync.RWMutex
	stopCh              chan struct{}
	wg                  sync.WaitGroup
}

// NewAdaptiveCircuitBreaker 创建新的自适应断路器
func NewAdaptiveCircuitBreaker(name string, opts AdaptiveOptions) *AdaptiveCircuitBreaker {
	// 使用基础配置创建标准断路器
	cb := New(name, opts.BaseOptions)

	acb := &AdaptiveCircuitBreaker{
		CircuitBreaker:      cb,
		opts:                opts,
		responseTimeSamples: make([]time.Duration, 100), // 保存最近100个响应时间样本
		errorPatterns:       make(map[string]*ErrorPattern),
		stopCh:              make(chan struct{}),
		metrics: AdaptiveMetrics{
			CurrentState:     StateClosed,
			CurrentThreshold: opts.BaseOptions.Threshold,
			CurrentTimeout:   opts.BaseOptions.Timeout,
			LastAdaptTime:    time.Now(),
		},
	}

	// 添加状态变更监听器
	cb.AddListener(func(name string, from, to State) {
		// 调用用户提供的回调
		if acb.opts.OnStateChangeCallback != nil {
			acb.metricsMu.RLock()
			metrics := acb.metrics
			acb.metricsMu.RUnlock()
			acb.opts.OnStateChangeCallback(name, from, to, metrics)
		}

		// 更新内部状态
		acb.metricsMu.Lock()
		acb.metrics.CurrentState = to
		acb.metricsMu.Unlock()

		// 状态变为Open时，运行错误模式分析
		if to == StateOpen && acb.opts.EnableErrorPatternAnalysis {
			acb.analyzeErrorPatterns()
		}
	})

	// 启动自适应调整协程
	if opts.AdaptInterval > 0 {
		acb.wg.Add(1)
		go acb.adaptiveAdjustmentLoop()
	}

	return acb
}

// Allow 检查请求是否被允许通过断路器
func (acb *AdaptiveCircuitBreaker) Allow() error {
	atomic.AddUint64(&acb.metrics.TotalRequests, 1)
	atomic.AddUint32(&acb.metrics.RequestCount5m, 1)

	// 使用基础断路器的检查
	return acb.CircuitBreaker.Allow()
}

// Success 记录成功请求，并更新断路器状态
func (acb *AdaptiveCircuitBreaker) Success() {
	start := time.Now()
	// 调用基础断路器的Success方法
	acb.CircuitBreaker.Success()

	// 记录响应时间
	responseTime := time.Since(start)
	acb.recordResponseTime(responseTime)

	// 更新指标
	acb.metricsMu.Lock()
	acb.metrics.ConsecutiveSuccesses++
	acb.metrics.ConsecutiveFailures = 0
	acb.metrics.HalfOpenAllowed = acb.halfOpenAllowed // 从基础断路器同步
	acb.metricsMu.Unlock()
}

// Failure 记录失败请求，并更新断路器状态
func (acb *AdaptiveCircuitBreaker) Failure() {
	// 调用基础断路器的Failure方法
	acb.CircuitBreaker.Failure()

	atomic.AddUint64(&acb.metrics.TotalFailures, 1)
	atomic.AddUint32(&acb.metrics.FailureCount5m, 1)

	// 更新指标
	acb.metricsMu.Lock()
	acb.metrics.ConsecutiveFailures++
	acb.metrics.ConsecutiveSuccesses = 0
	acb.metrics.FailureRate = float64(acb.metrics.FailureCount5m) / float64(acb.metrics.RequestCount5m)
	acb.metricsMu.Unlock()
}

// RecordEnhancedFailure 记录带错误详情的失败
func (acb *AdaptiveCircuitBreaker) RecordEnhancedFailure(err errs.EnhancedError) {
	acb.Failure()

	if !acb.opts.EnableErrorPatternAnalysis || err == nil {
		return
	}

	// 记录错误模式
	errorKey := string(err.Category()) + ":" + err.Code()

	acb.errorPatternMu.Lock()
	defer acb.errorPatternMu.Unlock()

	now := time.Now()
	if pattern, exists := acb.errorPatterns[errorKey]; exists {
		pattern.Count++
		pattern.LastSeen = now
		pattern.SuggestedStrategy = err.RecoveryStrategy()
	} else {
		acb.errorPatterns[errorKey] = &ErrorPattern{
			Category:          err.Category(),
			Code:              err.Code(),
			Count:             1,
			FirstSeen:         now,
			LastSeen:          now,
			SuggestedStrategy: err.RecoveryStrategy(),
		}
	}
}

// Execute 执行请求，包含断路器逻辑
func (acb *AdaptiveCircuitBreaker) Execute(request func() (interface{}, error)) (interface{}, error) {
	// 检查是否允许请求通过
	if err := acb.Allow(); err != nil {
		// 熔断时使用降级处理
		if acb.opts.FallbackHandler != nil {
			return acb.opts.FallbackHandler(nil)
		}
		return nil, err
	}

	// 执行实际请求
	startTime := time.Now()
	result, err := request()
	responseTime := time.Since(startTime)

	// 记录响应时间
	acb.recordResponseTime(responseTime)

	// 根据结果更新断路器状态
	if err != nil {
		// 处理增强错误
		if enhancedErr, ok := err.(errs.EnhancedError); ok {
			acb.RecordEnhancedFailure(enhancedErr)
		} else {
			acb.Failure()
		}

		// 如果提供了降级处理且错误不是由于断路器引起的
		if acb.opts.FallbackHandler != nil && err != ErrOpenState {
			return acb.opts.FallbackHandler(result)
		}

		return result, err
	}

	acb.Success()
	return result, nil
}

// recordResponseTime 记录响应时间样本
func (acb *AdaptiveCircuitBreaker) recordResponseTime(duration time.Duration) {
	acb.sampleMu.Lock()
	defer acb.sampleMu.Unlock()

	acb.responseTimeSamples[acb.sampleIndex] = duration
	acb.sampleIndex = (acb.sampleIndex + 1) % len(acb.responseTimeSamples)

	// 更新平均响应时间
	var total time.Duration
	var count int
	for _, d := range acb.responseTimeSamples {
		if d > 0 {
			total += d
			count++
		}
	}

	if count > 0 {
		acb.metricsMu.Lock()
		acb.metrics.AverageResponseTime = total / time.Duration(count)
		acb.metricsMu.Unlock()
	}
}

// adaptiveAdjustmentLoop 自适应调整参数的后台协程
func (acb *AdaptiveCircuitBreaker) adaptiveAdjustmentLoop() {
	defer acb.wg.Done()

	ticker := time.NewTicker(acb.opts.AdaptInterval)
	defer ticker.Stop()

	// 周期性重置5分钟计数器
	resetTicker := time.NewTicker(5 * time.Minute)
	defer resetTicker.Stop()

	for {
		select {
		case <-acb.stopCh:
			return
		case <-ticker.C:
			acb.adjustParameters()
		case <-resetTicker.C:
			// 重置5分钟计数器
			atomic.StoreUint32(&acb.metrics.RequestCount5m, 0)
			atomic.StoreUint32(&acb.metrics.FailureCount5m, 0)
		}
	}
}

// adjustParameters 根据当前状态调整断路器参数
func (acb *AdaptiveCircuitBreaker) adjustParameters() {
	acb.metricsMu.RLock()
	failureRate := acb.metrics.FailureRate
	avgResponseTime := acb.metrics.AverageResponseTime
	currentThreshold := acb.metrics.CurrentThreshold
	currentTimeout := acb.metrics.CurrentTimeout
	acb.metricsMu.RUnlock()

	acb.mutex.Lock()
	defer acb.mutex.Unlock()

	// 只有在关闭状态才调整参数，避免在开路或半开状态下变更配置
	if acb.state != StateClosed {
		return
	}

	newThreshold := currentThreshold
	newTimeout := currentTimeout

	// 根据失败率调整阈值
	if failureRate > 0.1 { // 失败率超过10%，提高阈值使断路器更难触发
		newThreshold = uint32(float64(currentThreshold) * acb.opts.IncreaseThresholdRate)
	} else if failureRate < 0.01 { // 失败率低于1%，降低阈值使断路器更易触发异常情况
		newThreshold = uint32(float64(currentThreshold) * acb.opts.DecreaseThresholdRate)
	}

	// 根据响应时间调整超时时间
	// 获取基准响应时间（可以是配置的或历史平均值）
	baselineResponseTime := 100 * time.Millisecond // 假设基准响应时间为100ms

	if avgResponseTime > 2*baselineResponseTime {
		// 响应时间显著增加，增加超时时间
		newTimeout = currentTimeout * 11 / 10 // 增加10%
	} else if avgResponseTime < baselineResponseTime/2 {
		// 响应时间显著减少，减少超时时间
		newTimeout = currentTimeout * 9 / 10 // 减少10%
	}

	// 确保参数在配置的范围内
	if newThreshold < acb.opts.MinThreshold {
		newThreshold = acb.opts.MinThreshold
	} else if newThreshold > acb.opts.MaxThreshold {
		newThreshold = acb.opts.MaxThreshold
	}

	if newTimeout < acb.opts.MinTimeout {
		newTimeout = acb.opts.MinTimeout
	} else if newTimeout > acb.opts.MaxTimeout {
		newTimeout = acb.opts.MaxTimeout
	}

	// 应用新参数
	if newThreshold != currentThreshold || newTimeout != currentTimeout {
		acb.opts.BaseOptions.Threshold = newThreshold
		acb.opts.BaseOptions.Timeout = newTimeout

		// 更新指标
		acb.metricsMu.Lock()
		acb.metrics.CurrentThreshold = newThreshold
		acb.metrics.CurrentTimeout = newTimeout
		acb.metrics.LastAdaptTime = time.Now()
		acb.metricsMu.Unlock()
	}
}

// analyzeErrorPatterns 分析错误模式，提供恢复建议
func (acb *AdaptiveCircuitBreaker) analyzeErrorPatterns() {
	acb.errorPatternMu.RLock()
	defer acb.errorPatternMu.RUnlock()

	// 根据错误模式调整恢复策略
	var patterns []ErrorPattern

	// 复制错误模式供指标使用
	for _, pattern := range acb.errorPatterns {
		patterns = append(patterns, *pattern)
	}

	// 更新指标
	acb.metricsMu.Lock()
	acb.metrics.ErrorPatterns = patterns
	acb.metricsMu.Unlock()

	// 根据错误模式可以进行更复杂的分析和调整
	// 例如，对于特定类型的错误模式，采取特定的恢复策略
}

// GetMetrics 获取当前指标
func (acb *AdaptiveCircuitBreaker) GetMetrics() AdaptiveMetrics {
	acb.metricsMu.RLock()
	defer acb.metricsMu.RUnlock()

	// 返回指标副本，避免并发访问问题
	metrics := acb.metrics

	// 复制错误模式切片
	if len(acb.metrics.ErrorPatterns) > 0 {
		metrics.ErrorPatterns = make([]ErrorPattern, len(acb.metrics.ErrorPatterns))
		copy(metrics.ErrorPatterns, acb.metrics.ErrorPatterns)
	}

	return metrics
}

// Reset 重置断路器状态和计数
func (acb *AdaptiveCircuitBreaker) Reset() {
	// 重置基础断路器
	acb.CircuitBreaker.Reset()

	// 重置自适应指标
	acb.metricsMu.Lock()
	acb.metrics.ConsecutiveSuccesses = 0
	acb.metrics.ConsecutiveFailures = 0
	acb.metrics.CurrentState = StateClosed
	acb.metricsMu.Unlock()

	// 重置错误模式
	acb.errorPatternMu.Lock()
	acb.errorPatterns = make(map[string]*ErrorPattern)
	acb.errorPatternMu.Unlock()

	// 重置响应时间样本
	acb.sampleMu.Lock()
	for i := range acb.responseTimeSamples {
		acb.responseTimeSamples[i] = 0
	}
	acb.sampleIndex = 0
	acb.sampleMu.Unlock()
}

// Close 关闭断路器并释放资源
func (acb *AdaptiveCircuitBreaker) Close() {
	close(acb.stopCh)
	acb.wg.Wait()
}
