package connpool

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/connectivity"
)

// AdaptiveOptions 自适应连接池选项
type AdaptiveOptions struct {
	// 基础连接池选项
	BaseOptions Options

	// 健康检查相关
	HealthCheckInterval time.Duration // 健康检查间隔
	HealthCheckTimeout  time.Duration // 健康检查超时

	// 自适应调整相关
	EnableAdaptive      bool          // 是否启用自适应调整
	LoadCheckInterval   time.Duration // 负载检查间隔
	HighLoadThreshold   float64       // 高负载阈值 (0.0-1.0)
	LowLoadThreshold    float64       // 低负载阈值 (0.0-1.0)
	ScaleUpFactor       float64       // 扩容因子 (如1.2表示增加20%)
	ScaleDownFactor     float64       // 缩容因子 (如0.8表示减少20%)
	MinConnections      int           // 最小连接数
	MaxConnections      int           // 最大连接数
	ScaleCooldownPeriod time.Duration // 两次缩放操作之间的冷却期
}

// DefaultAdaptiveOptions 返回默认的自适应连接池选项
func DefaultAdaptiveOptions() AdaptiveOptions {
	return AdaptiveOptions{
		BaseOptions: Options{
			MaxIdle:     5,
			MaxActive:   10,
			IdleTimeout: 60 * time.Second,
			WaitTimeout: 3 * time.Second,
			Wait:        true,
		},
		HealthCheckInterval: 30 * time.Second,
		HealthCheckTimeout:  5 * time.Second,
		EnableAdaptive:      true,
		LoadCheckInterval:   60 * time.Second,
		HighLoadThreshold:   0.8, // 80%负载认为是高负载
		LowLoadThreshold:    0.3, // 30%负载认为是低负载
		ScaleUpFactor:       1.5, // 扩容50%
		ScaleDownFactor:     0.8, // 缩容20%
		MinConnections:      5,
		MaxConnections:      100,
		ScaleCooldownPeriod: 3 * time.Minute,
	}
}

// AdaptivePool 自适应连接池实现
type AdaptivePool struct {
	// 内部基础连接池
	basePool *connPool
	// 配置选项
	opts AdaptiveOptions
	// 统计信息
	stats struct {
		successRequests uint64
		failedRequests  uint64
		timeouts        uint64
		healthFailures  uint64
		scaleUpEvents   uint32
		scaleDownEvents uint32
	}
	// 健康检查停止通道
	stopHealthCheck chan struct{}
	// 负载检查停止通道
	stopLoadCheck chan struct{}
	// 上次缩放时间
	lastScaleTime time.Time
	// 互斥锁
	mu sync.RWMutex
}

// NewAdaptivePool 创建新的自适应连接池
func NewAdaptivePool(target string, factory ConnectionFactory, opts AdaptiveOptions) Pool {
	// 创建基础连接池
	basePool := &connPool{
		opts:      opts.BaseOptions,
		factory:   factory,
		target:    target,
		idle:      make([]*PoolConn, 0, opts.BaseOptions.MaxIdle),
		cleanerCh: make(chan struct{}),
	}
	basePool.cond = sync.NewCond(&basePool.mu)

	// 启动空闲连接清理器
	if opts.BaseOptions.IdleTimeout > 0 {
		go basePool.cleaner()
	}

	ap := &AdaptivePool{
		basePool:        basePool,
		opts:            opts,
		stopHealthCheck: make(chan struct{}),
		stopLoadCheck:   make(chan struct{}),
		lastScaleTime:   time.Now(),
	}

	// 启动健康检查
	if opts.HealthCheckInterval > 0 {
		go ap.healthChecker()
	}

	// 启动负载监控和自适应调整
	if opts.EnableAdaptive && opts.LoadCheckInterval > 0 {
		go ap.loadMonitor()
	}

	return ap
}

// Get 从连接池获取一个连接
func (ap *AdaptivePool) Get(ctx context.Context) (*PoolConn, error) {
	conn, err := ap.basePool.Get(ctx)
	if err != nil {
		atomic.AddUint64(&ap.stats.failedRequests, 1)
		if err == ErrTimeout {
			atomic.AddUint64(&ap.stats.timeouts, 1)
		}
		return nil, err
	}
	atomic.AddUint64(&ap.stats.successRequests, 1)

	// 包装连接，以便于追踪归还事件
	return conn, nil
}

// Put 归还一个连接到池中
func (ap *AdaptivePool) Put(conn *PoolConn) error {
	return ap.basePool.Put(conn)
}

// Close 关闭连接池
func (ap *AdaptivePool) Close() error {
	// 停止健康检查和负载监控
	close(ap.stopHealthCheck)
	close(ap.stopLoadCheck)
	return ap.basePool.Close()
}

// Status 返回连接池状态
func (ap *AdaptivePool) Status() Status {
	baseStatus := ap.basePool.Status()
	return baseStatus
}

// GetExtendedStatus 返回扩展的连接池状态信息
func (ap *AdaptivePool) GetExtendedStatus() AdaptiveStatus {
	baseStatus := ap.basePool.Status()

	return AdaptiveStatus{
		Status:          baseStatus,
		SuccessCount:    atomic.LoadUint64(&ap.stats.successRequests),
		FailureCount:    atomic.LoadUint64(&ap.stats.failedRequests),
		TimeoutCount:    atomic.LoadUint64(&ap.stats.timeouts),
		HealthFailures:  atomic.LoadUint64(&ap.stats.healthFailures),
		ScaleUpEvents:   atomic.LoadUint32(&ap.stats.scaleUpEvents),
		ScaleDownEvents: atomic.LoadUint32(&ap.stats.scaleDownEvents),
	}
}

// AdaptiveStatus 扩展的连接池状态
type AdaptiveStatus struct {
	Status          Status
	SuccessCount    uint64
	FailureCount    uint64
	TimeoutCount    uint64
	HealthFailures  uint64
	ScaleUpEvents   uint32
	ScaleDownEvents uint32
}

// healthChecker 定期检查连接健康状态
func (ap *AdaptivePool) healthChecker() {
	ticker := time.NewTicker(ap.opts.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ap.checkHealth()
		case <-ap.stopHealthCheck:
			return
		}
	}
}

// checkHealth 检查所有空闲连接的健康状态
func (ap *AdaptivePool) checkHealth() {
	ap.basePool.mu.Lock()
	defer ap.basePool.mu.Unlock()

	// 创建新的空闲连接列表，仅保留健康的连接
	healthyIdle := make([]*PoolConn, 0, len(ap.basePool.idle))
	unhealthyCount := 0

	for _, conn := range ap.basePool.idle {
		ctx, cancel := context.WithTimeout(context.Background(), ap.opts.HealthCheckTimeout)
		state := conn.ClientConn.GetState()

		// 检查连接状态
		if state == connectivity.Ready || state == connectivity.Idle {
			// 连接健康，保留
			healthyIdle = append(healthyIdle, conn)
		} else if state == connectivity.TransientFailure || state == connectivity.Shutdown {
			// 连接不健康，关闭并移除
			conn.ClientConn.Close()
			unhealthyCount++
		} else {
			// 对于其他状态，尝试等待连接就绪
			if !conn.ClientConn.WaitForStateChange(ctx, state) {
				// 超时，关闭连接
				conn.ClientConn.Close()
				unhealthyCount++
			} else if newState := conn.ClientConn.GetState(); newState == connectivity.Ready || newState == connectivity.Idle {
				// 状态已变为健康
				healthyIdle = append(healthyIdle, conn)
			} else {
				// 状态变化但仍不健康
				conn.ClientConn.Close()
				unhealthyCount++
			}
		}

		cancel()
	}

	// 更新统计和空闲连接列表
	if unhealthyCount > 0 {
		atomic.AddUint64(&ap.stats.healthFailures, uint64(unhealthyCount))
		ap.basePool.idle = healthyIdle
	}
}

// loadMonitor 监控连接池负载并进行自适应调整
func (ap *AdaptivePool) loadMonitor() {
	ticker := time.NewTicker(ap.opts.LoadCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ap.checkAndAdjustPool()
		case <-ap.stopLoadCheck:
			return
		}
	}
}

// checkAndAdjustPool 检查负载并调整连接池大小
func (ap *AdaptivePool) checkAndAdjustPool() {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// 检查冷却期
	if time.Since(ap.lastScaleTime) < ap.opts.ScaleCooldownPeriod {
		return
	}

	status := ap.basePool.Status()
	currentMaxActive := status.MaxActive

	// 计算当前负载
	var loadFactor float64
	if currentMaxActive > 0 {
		loadFactor = float64(status.ActiveCount) / float64(currentMaxActive)
	}

	var newMaxActive int

	// 根据负载因子调整大小
	if loadFactor >= ap.opts.HighLoadThreshold {
		// 高负载，扩容
		newMaxActive = int(float64(currentMaxActive) * ap.opts.ScaleUpFactor)
		if newMaxActive > ap.opts.MaxConnections {
			newMaxActive = ap.opts.MaxConnections
		}

		if newMaxActive > currentMaxActive {
			ap.basePool.mu.Lock()
			ap.basePool.opts.MaxActive = newMaxActive
			ap.basePool.mu.Unlock()

			atomic.AddUint32(&ap.stats.scaleUpEvents, 1)
			ap.lastScaleTime = time.Now()
			log.Printf("连接池扩容: %d -> %d (负载: %.2f)", currentMaxActive, newMaxActive, loadFactor)
		}
	} else if loadFactor <= ap.opts.LowLoadThreshold && status.WaitCount == 0 {
		// 低负载，缩容
		newMaxActive = int(float64(currentMaxActive) * ap.opts.ScaleDownFactor)
		if newMaxActive < ap.opts.MinConnections {
			newMaxActive = ap.opts.MinConnections
		}

		if newMaxActive < currentMaxActive {
			ap.basePool.mu.Lock()
			ap.basePool.opts.MaxActive = newMaxActive
			ap.basePool.mu.Unlock()

			atomic.AddUint32(&ap.stats.scaleDownEvents, 1)
			ap.lastScaleTime = time.Now()
			log.Printf("连接池缩容: %d -> %d (负载: %.2f)", currentMaxActive, newMaxActive, loadFactor)
		}
	}
}

// 预热连接池，创建一定数量的初始连接
func (ap *AdaptivePool) Preconnect(ctx context.Context, count int) error {
	// 创建临时连接切片存储预连接
	conns := make([]*PoolConn, 0, count)
	var err error

	// 尝试创建指定数量的连接
	for i := 0; i < count; i++ {
		var conn *PoolConn
		conn, err = ap.Get(ctx)
		if err != nil {
			break
		}
		conns = append(conns, conn)
	}

	// 将创建的连接归还池中
	for _, conn := range conns {
		_ = ap.Put(conn)
	}

	return err
}
