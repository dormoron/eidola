package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State 表示断路器的状态
type State int

const (
	// 断路器关闭状态，允许请求通过
	StateClosed State = iota
	// 断路器半开状态，允许有限请求通过以测试服务是否恢复
	StateHalfOpen
	// 断路器开路状态，所有请求都会立即失败
	StateOpen
)

// String 返回断路器状态的字符串表示
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

var (
	// ErrOpenState 表示断路器处于开路状态，请求被拒绝
	ErrOpenState = errors.New("circuit breaker is open")
	// ErrTooManyConcurrent 表示并发请求数超过限制
	ErrTooManyConcurrent = errors.New("too many concurrent requests")
)

// StateChangeListener 状态变更监听器
type StateChangeListener func(name string, from, to State)

// Options 配置断路器参数
type Options struct {
	// 触发断路器的连续失败次数阈值
	Threshold uint32
	// 断路器从开路状态转为半开状态的超时时间
	Timeout time.Duration
	// 半开状态时允许通过的请求数初始值
	HalfOpenMaxRequests uint32
	// 渐进增长因子，半开状态成功后允许通过的请求数增长因子
	ProgressiveFactor float64
	// 最大并发请求数，0表示不限制
	MaxConcurrent uint32
	// 失败请求的滑动窗口大小
	WindowSize time.Duration
	// 成功请求所需的连续成功次数
	SuccessThreshold uint32
	// 开路时是否立即终止正在进行的请求
	ForceBreak bool
	// 状态变更监听器
	OnStateChange StateChangeListener
}

// DefaultOptions 返回默认的断路器配置
func DefaultOptions() Options {
	return Options{
		Threshold:           5,
		Timeout:             5 * time.Second,
		HalfOpenMaxRequests: 1,
		ProgressiveFactor:   2.0, // 默认每次成功后倍增
		MaxConcurrent:       0,
		WindowSize:          10 * time.Second,
		SuccessThreshold:    2,
		ForceBreak:          false,
		OnStateChange:       nil,
	}
}

// CircuitBreaker 实现断路器模式
type CircuitBreaker struct {
	name                string
	opts                Options
	state               State
	failures            uint32
	successes           uint32
	lastStateChange     time.Time
	halfOpenAllowed     uint32 // 当前半开状态允许的请求数
	halfOpenOriginal    uint32 // 原始半开状态请求数配置
	concurrent          uint32
	mutex               sync.RWMutex
	failures_window     []time.Time
	listeners           []StateChangeListener // 状态变更监听器列表
	lastFailure         time.Time             // 上次失败时间
	consecutiveFailures uint32                // 连续失败次数
}

// New 创建一个新的断路器
func New(name string, opts Options) *CircuitBreaker {
	return &CircuitBreaker{
		name:             name,
		opts:             opts,
		state:            StateClosed,
		failures:         0,
		successes:        0,
		lastStateChange:  time.Now(),
		failures_window:  make([]time.Time, 0),
		halfOpenOriginal: opts.HalfOpenMaxRequests,
		halfOpenAllowed:  opts.HalfOpenMaxRequests,
		listeners:        []StateChangeListener{opts.OnStateChange},
	}
}

// AddListener 添加状态变更监听器
func (cb *CircuitBreaker) AddListener(listener StateChangeListener) {
	if listener == nil {
		return
	}

	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.listeners = append(cb.listeners, listener)
}

// Allow 检查请求是否被允许通过断路器
func (cb *CircuitBreaker) Allow() error {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()

	// 处理并发请求限制
	if cb.opts.MaxConcurrent > 0 && cb.concurrent >= cb.opts.MaxConcurrent {
		return ErrTooManyConcurrent
	}

	// 按照断路器状态处理
	switch cb.state {
	case StateClosed:
		cb.concurrent++
		return nil

	case StateOpen:
		// 检查是否应该转为半开状态
		if now.Sub(cb.lastStateChange) > cb.opts.Timeout {
			cb.changeState(StateHalfOpen, now)
			cb.concurrent++
			return nil
		}
		return ErrOpenState

	case StateHalfOpen:
		// 在半开状态下，只允许有限数量的请求通过
		if cb.halfOpenAllowed > 0 {
			cb.halfOpenAllowed--
			cb.concurrent++
			return nil
		}
		return ErrOpenState

	default:
		// 未知状态，允许请求通过，但重置为关闭状态
		cb.changeState(StateClosed, now)
		cb.concurrent++
		return nil
	}
}

// Success 记录成功请求，并更新断路器状态
func (cb *CircuitBreaker) Success() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.concurrent > 0 {
		cb.concurrent--
	}

	now := time.Now()

	// 重置连续失败计数
	cb.consecutiveFailures = 0

	// 在半开状态下的成功处理
	if cb.state == StateHalfOpen {
		cb.successes++
		if cb.successes >= cb.opts.SuccessThreshold {
			// 连续成功次数达到阈值，关闭断路器
			cb.changeState(StateClosed, now)
		} else {
			// 成功但未达到阈值，增加允许通过的请求数（渐进式恢复）
			newAllowed := uint32(float64(cb.halfOpenOriginal) * cb.opts.ProgressiveFactor)
			if newAllowed > 0 {
				cb.halfOpenAllowed += newAllowed
			}
		}
		return
	}

	// 其他状态下，简单地记录成功
	cb.failures = 0
	cb.failures_window = make([]time.Time, 0)
}

// Failure 记录失败请求，并更新断路器状态
func (cb *CircuitBreaker) Failure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.concurrent > 0 {
		cb.concurrent--
	}

	now := time.Now()
	cb.lastFailure = now
	cb.consecutiveFailures++

	// 在时间窗口内记录失败
	if cb.opts.WindowSize > 0 {
		// 添加当前失败
		cb.failures_window = append(cb.failures_window, now)

		// 移除窗口外的失败记录
		cutoff := now.Add(-cb.opts.WindowSize)
		var newWindow []time.Time
		for _, t := range cb.failures_window {
			if t.After(cutoff) {
				newWindow = append(newWindow, t)
			}
		}
		cb.failures_window = newWindow
		cb.failures = uint32(len(cb.failures_window))
	} else {
		// 不使用窗口时，直接增加失败计数
		cb.failures++
	}

	// 在半开状态下的失败处理
	if cb.state == StateHalfOpen {
		// 半开状态下任何失败都会重新开路
		cb.changeState(StateOpen, now)
		return
	}

	// 在关闭状态下，检查是否应该打开断路器
	if cb.state == StateClosed && (cb.failures >= cb.opts.Threshold || cb.consecutiveFailures >= cb.opts.Threshold) {
		cb.changeState(StateOpen, now)
	}
}

// changeState 更改断路器状态
func (cb *CircuitBreaker) changeState(newState State, now time.Time) {
	oldState := cb.state
	cb.state = newState
	cb.lastStateChange = now

	// 状态更改时重置计数器
	if newState == StateClosed {
		cb.failures = 0
		cb.failures_window = make([]time.Time, 0)
		cb.consecutiveFailures = 0
	} else if newState == StateHalfOpen {
		cb.successes = 0
		cb.halfOpenAllowed = cb.halfOpenOriginal
	}

	// 通知状态变更
	if oldState != newState {
		for _, listener := range cb.listeners {
			if listener != nil {
				go listener(cb.name, oldState, newState)
			}
		}
	}
}

// ForceOpen 强制断路器进入开路状态
func (cb *CircuitBreaker) ForceOpen() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.changeState(StateOpen, time.Now())
}

// ForceClosed 强制断路器进入关闭状态
func (cb *CircuitBreaker) ForceClosed() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.changeState(StateClosed, time.Now())
}

// Reset 重置断路器状态
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.changeState(StateClosed, time.Now())
	cb.failures = 0
	cb.successes = 0
	cb.failures_window = make([]time.Time, 0)
	cb.halfOpenAllowed = cb.halfOpenOriginal
	cb.concurrent = 0
	cb.consecutiveFailures = 0
}

// State 返回当前断路器状态
func (cb *CircuitBreaker) State() State {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

// Stats 返回断路器的统计信息
type Stats struct {
	State               State
	Failures            uint32
	ConsecutiveFailures uint32
	Successes           uint32
	CurrentRequests     uint32
	Since               time.Duration
	LastFailure         time.Time
	HalfOpenAllowed     uint32 // 半开状态下当前允许的请求数
}

// Stats 返回断路器的当前统计信息
func (cb *CircuitBreaker) Stats() Stats {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	return Stats{
		State:               cb.state,
		Failures:            cb.failures,
		ConsecutiveFailures: cb.consecutiveFailures,
		Successes:           cb.successes,
		CurrentRequests:     cb.concurrent,
		Since:               time.Since(cb.lastStateChange),
		LastFailure:         cb.lastFailure,
		HalfOpenAllowed:     cb.halfOpenAllowed,
	}
}

// InjectFailure 注入一个失败，用于测试断路器
func (cb *CircuitBreaker) InjectFailure() {
	cb.Failure()
}

// 检查断路器是否生效
func (cb *CircuitBreaker) IsActive() bool {
	return cb.state == StateOpen || cb.state == StateHalfOpen
}

// ApplyOptions 更新断路器配置
func (cb *CircuitBreaker) ApplyOptions(opts Options) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.opts = opts
	cb.halfOpenOriginal = opts.HalfOpenMaxRequests

	// 如果有新的监听器，添加到列表中
	if opts.OnStateChange != nil {
		// 函数无法直接比较，只能添加新的监听器
		cb.listeners = append(cb.listeners, opts.OnStateChange)
	}
}

// Name 返回断路器名称
func (cb *CircuitBreaker) Name() string {
	return cb.name
}
