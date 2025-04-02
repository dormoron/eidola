package batch

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrBatchClosed 表示批处理器已关闭
	ErrBatchClosed = errors.New("batch processor is closed")
	// ErrRequestTimeout 表示请求等待批处理超时
	ErrRequestTimeout = errors.New("request timeout waiting for batch processing")
	// ErrBatchSizeLimitExceeded 表示请求太大，超过批处理大小限制
	ErrBatchSizeLimitExceeded = errors.New("request size exceeds batch size limit")
	// ErrContextCanceled 表示请求上下文被取消
	ErrContextCanceled = errors.New("request context was canceled")
)

// AdaptiveBatchOptions 自适应批处理器配置
type AdaptiveBatchOptions struct {
	// 最大批处理大小（条目数）
	MaxBatchSize int
	// 触发处理的最小批处理大小
	MinBatchSize int
	// 等待形成批处理的最大时间
	MaxLatency time.Duration
	// 单个请求的最大大小 (0表示无限制)
	MaxRequestSize int
	// 允许的最大并发批处理数
	MaxConcurrentBatches int
	// 负载基础批处理大小调整
	// 高负载时的批处理大小倍率 (0-1，低于1表示减小批处理大小)
	HighLoadFactor float64
	// 低负载时的批处理大小倍率 (>1，大于1表示增加批处理大小)
	LowLoadFactor float64
	// 负载判断阈值 (0-1)，超过此值认为是高负载
	HighLoadThreshold float64
	// 负载判断阈值 (0-1)，低于此值认为是低负载
	LowLoadThreshold float64
	// 请求大小估算函数，若为nil则所有请求视为相同大小
	RequestSizeFunc func(interface{}) int
	// 请求合并函数，将多个请求合并为一个批处理请求
	BatchRequestFunc func([]interface{}) interface{}
	// 响应拆分函数，将批处理响应拆分为多个单独响应
	SplitResponseFunc func(interface{}, int) []interface{}
	// 错误处理函数，处理批处理过程中的错误
	ErrorHandler func(error) error
	// 统计收集间隔
	StatsInterval time.Duration
}

// DefaultAdaptiveBatchOptions 返回默认的自适应批处理配置
func DefaultAdaptiveBatchOptions() AdaptiveBatchOptions {
	return AdaptiveBatchOptions{
		MaxBatchSize:         100,
		MinBatchSize:         10,
		MaxLatency:           50 * time.Millisecond,
		MaxConcurrentBatches: 10,
		HighLoadFactor:       0.7, // 高负载时减小批处理大小至正常的70%
		LowLoadFactor:        1.5, // 低负载时增加批处理大小至正常的150%
		HighLoadThreshold:    0.8, // 利用率超过80%视为高负载
		LowLoadThreshold:     0.3, // 利用率低于30%视为低负载
		StatsInterval:        5 * time.Second,
	}
}

// RequestItem 表示批处理中的单个请求项
type RequestItem struct {
	Request      interface{}
	Size         int
	Response     interface{}
	Error        error
	Done         chan struct{}
	ctx          context.Context
	arrivedAt    time.Time
	processingAt time.Time
	completedAt  time.Time
}

// BatchStats 批处理统计信息
type BatchStats struct {
	// 总请求数
	TotalRequests int64
	// 总批次数
	TotalBatches int64
	// 平均批处理大小
	AvgBatchSize float64
	// 平均请求等待时间
	AvgWaitTimeMs float64
	// 平均处理时间
	AvgProcessTimeMs float64
	// 当前批处理大小
	CurrentBatchSize int
	// 当前等待请求数
	CurrentWaitingRequests int
	// 当前处理中的批次数
	CurrentActiveBatches int
	// 负载系数（0-1）
	LoadFactor float64
	// 批处理率（每秒批次数）
	BatchRate float64
	// 请求率（每秒请求数）
	RequestRate float64
}

// AdaptiveBatcher 自适应批处理器
type AdaptiveBatcher struct {
	opts              AdaptiveBatchOptions
	processor         BatchProcessor
	items             []*RequestItem
	itemsMu           sync.Mutex
	activeCount       int
	activeCountMu     sync.Mutex
	closed            bool
	closedMu          sync.RWMutex
	timer             *time.Timer
	timerMu           sync.Mutex
	timerActive       bool
	currentBatchSize  int
	stats             BatchStats
	statsMu           sync.RWMutex
	requestChan       chan *RequestItem
	stop              chan struct{}
	processingCount   int64
	processingCountMu sync.Mutex
	lastProcessTime   time.Time
	requestTimes      []time.Time
	requestTimesMu    sync.Mutex
	requestTimeIdx    int
	batchTimes        []time.Time
	batchTimesMu      sync.Mutex
	batchTimeIdx      int
}

// BatchProcessor 批处理执行器接口
type BatchProcessor func(context.Context, interface{}) (interface{}, error)

// NewAdaptiveBatcher 创建新的自适应批处理器
func NewAdaptiveBatcher(processor BatchProcessor, opts AdaptiveBatchOptions) *AdaptiveBatcher {
	if opts.MaxBatchSize <= 0 {
		opts.MaxBatchSize = 100
	}
	if opts.MinBatchSize <= 0 {
		opts.MinBatchSize = 1
	}
	if opts.MaxConcurrentBatches <= 0 {
		opts.MaxConcurrentBatches = 10
	}
	if opts.MaxLatency <= 0 {
		opts.MaxLatency = 100 * time.Millisecond
	}

	b := &AdaptiveBatcher{
		opts:             opts,
		processor:        processor,
		items:            make([]*RequestItem, 0, opts.MaxBatchSize),
		currentBatchSize: opts.MaxBatchSize,
		requestChan:      make(chan *RequestItem, opts.MaxBatchSize*2),
		stop:             make(chan struct{}),
		timer:            time.NewTimer(opts.MaxLatency),
		requestTimes:     make([]time.Time, 100),
		batchTimes:       make([]time.Time, 100),
	}

	// 立即停止计时器，我们会在需要时重新启用它
	if !b.timer.Stop() {
		<-b.timer.C
	}

	// 启动处理协程
	go b.processLoop()

	// 启动统计收集协程
	if opts.StatsInterval > 0 {
		go b.statsLoop()
	}

	return b
}

// Process 添加一个请求到批处理
func (b *AdaptiveBatcher) Process(ctx context.Context, request interface{}) (interface{}, error) {
	// 检查批处理器是否已关闭
	b.closedMu.RLock()
	if b.closed {
		b.closedMu.RUnlock()
		return nil, ErrBatchClosed
	}
	b.closedMu.RUnlock()

	// 检查请求大小
	var size int
	if b.opts.RequestSizeFunc != nil {
		size = b.opts.RequestSizeFunc(request)
		if b.opts.MaxRequestSize > 0 && size > b.opts.MaxRequestSize {
			return nil, ErrBatchSizeLimitExceeded
		}
	}

	// 创建请求项
	item := &RequestItem{
		Request:   request,
		Size:      size,
		Done:      make(chan struct{}),
		ctx:       ctx,
		arrivedAt: time.Now(),
	}

	// 记录请求时间用于计算速率
	b.requestTimesMu.Lock()
	b.requestTimes[b.requestTimeIdx] = item.arrivedAt
	b.requestTimeIdx = (b.requestTimeIdx + 1) % len(b.requestTimes)
	b.requestTimesMu.Unlock()

	// 将请求发送到处理通道
	select {
	case b.requestChan <- item:
		// 请求已加入队列
	case <-ctx.Done():
		return nil, ErrContextCanceled
	}

	// 等待请求处理完成或上下文取消
	select {
	case <-item.Done:
		// 请求已处理完成
		return item.Response, item.Error
	case <-ctx.Done():
		// 上下文已取消，但请求可能仍在处理中
		// 我们不能从批处理中移除请求，只能标记为已取消
		return nil, ctx.Err()
	}
}

// processLoop 批处理处理循环
func (b *AdaptiveBatcher) processLoop() {
	for {
		select {
		case <-b.stop:
			// 停止信号
			return

		case item := <-b.requestChan:
			// 收到一个新请求
			b.itemsMu.Lock()

			// 添加请求到队列
			b.items = append(b.items, item)
			itemCount := len(b.items)

			// 检查是否应该立即处理
			var shouldProcess bool
			if itemCount >= b.currentBatchSize {
				shouldProcess = true
			} else if itemCount >= b.opts.MinBatchSize {
				// 检查第一个请求是否已经等待足够长的时间
				oldestTime := b.items[0].arrivedAt
				shouldProcess = time.Since(oldestTime) >= b.opts.MaxLatency
			}

			// 如果还没达到处理条件，启动计时器
			if !shouldProcess && !b.timerActive && itemCount > 0 {
				b.timerMu.Lock()
				b.timer.Reset(b.opts.MaxLatency)
				b.timerActive = true
				b.timerMu.Unlock()
			}

			// 更新统计信息
			b.statsMu.Lock()
			b.stats.CurrentWaitingRequests = itemCount
			b.statsMu.Unlock()

			// 如果需要立即处理，且未超过并发批次限制
			if shouldProcess {
				// 检查是否可以启动新的批处理
				canProcess := true
				b.activeCountMu.Lock()
				if b.activeCount >= b.opts.MaxConcurrentBatches {
					canProcess = false
				} else {
					b.activeCount++
				}
				b.activeCountMu.Unlock()

				if canProcess {
					// 提取当前批次并清空队列
					batch := b.items
					b.items = make([]*RequestItem, 0, b.currentBatchSize)
					b.itemsMu.Unlock()

					// 启动新协程处理批次
					go b.processBatch(batch)
				} else {
					b.itemsMu.Unlock()
				}
			} else {
				b.itemsMu.Unlock()
			}

		case <-b.timer.C:
			// 计时器触发，处理当前批次
			b.timerMu.Lock()
			b.timerActive = false
			b.timerMu.Unlock()

			b.itemsMu.Lock()
			if len(b.items) > 0 {
				// 检查是否可以启动新的批处理
				canProcess := true
				b.activeCountMu.Lock()
				if b.activeCount >= b.opts.MaxConcurrentBatches {
					canProcess = false
				} else {
					b.activeCount++
				}
				b.activeCountMu.Unlock()

				if canProcess {
					// 提取当前批次并清空队列
					batch := b.items
					b.items = make([]*RequestItem, 0, b.currentBatchSize)
					b.itemsMu.Unlock()

					// 启动新协程处理批次
					go b.processBatch(batch)
				} else {
					b.itemsMu.Unlock()
				}
			} else {
				b.itemsMu.Unlock()
			}
		}
	}
}

// processBatch 处理一个批次的请求
func (b *AdaptiveBatcher) processBatch(items []*RequestItem) {
	if len(items) == 0 {
		return
	}

	// 记录批处理开始时间
	now := time.Now()
	b.batchTimesMu.Lock()
	b.batchTimes[b.batchTimeIdx] = now
	b.batchTimeIdx = (b.batchTimeIdx + 1) % len(b.batchTimes)
	b.batchTimesMu.Unlock()

	// 创建合并上下文
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 首先检查所有请求的上下文是否仍然有效
	var validItems []*RequestItem
	for _, item := range items {
		if item.ctx.Err() == nil {
			validItems = append(validItems, item)
			item.processingAt = now
		} else {
			// 请求上下文已取消
			item.Error = item.ctx.Err()
			close(item.Done)
		}
	}

	// 如果没有有效请求，直接返回
	if len(validItems) == 0 {
		b.activeCountMu.Lock()
		b.activeCount--
		b.activeCountMu.Unlock()
		return
	}

	// 合并请求
	var batchRequest interface{}
	if b.opts.BatchRequestFunc != nil {
		// 提取所有请求
		requests := make([]interface{}, len(validItems))
		for i, item := range validItems {
			requests[i] = item.Request
		}
		batchRequest = b.opts.BatchRequestFunc(requests)
	} else if len(validItems) == 1 {
		// 只有一个请求，不需要合并
		batchRequest = validItems[0].Request
	} else {
		// 没有合并函数但有多个请求，这是一个错误
		err := errors.New("multiple requests but no BatchRequestFunc provided")
		for _, item := range validItems {
			item.Error = err
			close(item.Done)
		}
		b.activeCountMu.Lock()
		b.activeCount--
		b.activeCountMu.Unlock()
		return
	}

	// 执行批处理
	b.processingCountMu.Lock()
	b.processingCount++
	b.processingCountMu.Unlock()

	batchResponse, err := b.processor(ctx, batchRequest)

	// 更新负载统计
	b.processingCountMu.Lock()
	b.processingCount--
	b.lastProcessTime = time.Now()
	b.processingCountMu.Unlock()

	// 更新统计信息
	b.statsMu.Lock()
	b.stats.TotalBatches++
	b.stats.TotalRequests += int64(len(validItems))

	// 动态调整批处理大小
	if b.stats.LoadFactor > b.opts.HighLoadThreshold {
		// 高负载，减小批处理大小
		newSize := int(float64(b.currentBatchSize) * b.opts.HighLoadFactor)
		if newSize < b.opts.MinBatchSize {
			newSize = b.opts.MinBatchSize
		}
		b.currentBatchSize = newSize
	} else if b.stats.LoadFactor < b.opts.LowLoadThreshold {
		// 低负载，增加批处理大小
		newSize := int(float64(b.currentBatchSize) * b.opts.LowLoadFactor)
		if newSize > b.opts.MaxBatchSize {
			newSize = b.opts.MaxBatchSize
		}
		b.currentBatchSize = newSize
	}

	// 更新当前批处理大小统计
	b.stats.CurrentBatchSize = b.currentBatchSize
	b.stats.CurrentActiveBatches = b.activeCount

	// 计算等待和处理时间
	var totalWaitTime int64
	var totalProcessTime int64

	for _, item := range validItems {
		item.completedAt = time.Now()
		waitTime := item.processingAt.Sub(item.arrivedAt).Milliseconds()
		processTime := item.completedAt.Sub(item.processingAt).Milliseconds()
		totalWaitTime += waitTime
		totalProcessTime += processTime
	}

	if len(validItems) > 0 {
		b.stats.AvgWaitTimeMs = float64(totalWaitTime) / float64(len(validItems))
		b.stats.AvgProcessTimeMs = float64(totalProcessTime) / float64(len(validItems))
		b.stats.AvgBatchSize = float64(b.stats.TotalRequests) / float64(b.stats.TotalBatches)
	}
	b.statsMu.Unlock()

	// 处理批处理错误
	if err != nil {
		if b.opts.ErrorHandler != nil {
			err = b.opts.ErrorHandler(err)
		}
		// 将错误分配给所有请求
		for _, item := range validItems {
			item.Error = err
			close(item.Done)
		}
	} else {
		// 拆分响应
		if b.opts.SplitResponseFunc != nil && len(validItems) > 1 {
			responses := b.opts.SplitResponseFunc(batchResponse, len(validItems))
			// 分配响应
			for i, item := range validItems {
				if i < len(responses) {
					item.Response = responses[i]
				} else {
					item.Error = errors.New("split response function returned fewer responses than requests")
				}
				close(item.Done)
			}
		} else if len(validItems) == 1 {
			// 只有一个请求，直接返回响应
			validItems[0].Response = batchResponse
			close(validItems[0].Done)
		} else {
			// 错误：多个请求但没有拆分函数
			err := errors.New("multiple requests but no SplitResponseFunc provided")
			for _, item := range validItems {
				item.Error = err
				close(item.Done)
			}
		}
	}

	// 减少活动批次计数
	b.activeCountMu.Lock()
	b.activeCount--
	b.activeCountMu.Unlock()
}

// statsLoop 定期更新统计信息
func (b *AdaptiveBatcher) statsLoop() {
	ticker := time.NewTicker(b.opts.StatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.updateStats()
		}
	}
}

// updateStats 更新统计信息
func (b *AdaptiveBatcher) updateStats() {
	// 计算负载系数
	b.processingCountMu.Lock()
	loadFactor := float64(b.processingCount) / float64(b.opts.MaxConcurrentBatches)
	lastProcessTime := b.lastProcessTime
	b.processingCountMu.Unlock()

	// 计算请求率
	var requestRate float64
	b.requestTimesMu.Lock()
	var validRequestTimes int
	var oldestRequestTime time.Time
	now := time.Now()
	for _, t := range b.requestTimes {
		if !t.IsZero() && now.Sub(t) < time.Minute {
			if oldestRequestTime.IsZero() || t.Before(oldestRequestTime) {
				oldestRequestTime = t
			}
			validRequestTimes++
		}
	}
	b.requestTimesMu.Unlock()

	if validRequestTimes > 0 && !oldestRequestTime.IsZero() {
		elapsed := now.Sub(oldestRequestTime).Seconds()
		if elapsed > 0 {
			requestRate = float64(validRequestTimes) / elapsed
		}
	}

	// 计算批处理率
	var batchRate float64
	b.batchTimesMu.Lock()
	var validBatchTimes int
	var oldestBatchTime time.Time
	for _, t := range b.batchTimes {
		if !t.IsZero() && now.Sub(t) < time.Minute {
			if oldestBatchTime.IsZero() || t.Before(oldestBatchTime) {
				oldestBatchTime = t
			}
			validBatchTimes++
		}
	}
	b.batchTimesMu.Unlock()

	if validBatchTimes > 0 && !oldestBatchTime.IsZero() {
		elapsed := now.Sub(oldestBatchTime).Seconds()
		if elapsed > 0 {
			batchRate = float64(validBatchTimes) / elapsed
		}
	}

	// 更新统计信息
	b.statsMu.Lock()
	if !lastProcessTime.IsZero() && time.Since(lastProcessTime) > time.Minute {
		// 长时间没有处理请求，重置负载因子
		loadFactor = 0
	}
	b.stats.LoadFactor = loadFactor
	b.stats.RequestRate = requestRate
	b.stats.BatchRate = batchRate
	b.statsMu.Unlock()
}

// GetStats 获取当前统计信息
func (b *AdaptiveBatcher) GetStats() BatchStats {
	b.statsMu.RLock()
	defer b.statsMu.RUnlock()
	return b.stats
}

// Flush 立即处理所有待处理的请求
func (b *AdaptiveBatcher) Flush() {
	b.itemsMu.Lock()
	if len(b.items) > 0 {
		batch := b.items
		b.items = make([]*RequestItem, 0, b.currentBatchSize)
		b.itemsMu.Unlock()

		b.processBatch(batch)
	} else {
		b.itemsMu.Unlock()
	}
}

// Close 关闭批处理器
func (b *AdaptiveBatcher) Close() {
	b.closedMu.Lock()
	defer b.closedMu.Unlock()

	if !b.closed {
		b.closed = true
		// 停止计时器
		b.timerMu.Lock()
		if b.timerActive {
			b.timer.Stop()
		}
		b.timerMu.Unlock()

		// 发送停止信号
		close(b.stop)

		// 处理所有剩余请求
		b.Flush()
	}
}

// MultiProcessor 用于并行处理批请求，适用于请求之间没有依赖关系的场景
type MultiProcessor interface {
	// ProcessMulti 处理多个独立的请求，返回对应的响应和错误
	ProcessMulti(ctx context.Context, requests []interface{}) ([]interface{}, []error)
}

// MultiProcessorFunc 将函数转换为MultiProcessor接口
type MultiProcessorFunc func(context.Context, []interface{}) ([]interface{}, []error)

// ProcessMulti 实现MultiProcessor接口
func (f MultiProcessorFunc) ProcessMulti(ctx context.Context, requests []interface{}) ([]interface{}, []error) {
	return f(ctx, requests)
}

// NewMultiProcessorBatcher 创建处理多个独立请求的批处理器
func NewMultiProcessorBatcher(processor MultiProcessor, opts AdaptiveBatchOptions) *AdaptiveBatcher {
	// 创建批处理函数
	batchProcessor := func(ctx context.Context, batchReq interface{}) (interface{}, error) {
		requests, ok := batchReq.([]interface{})
		if !ok {
			return nil, errors.New("invalid batch request type")
		}

		responses, errs := processor.ProcessMulti(ctx, requests)
		return struct {
			Responses []interface{}
			Errors    []error
		}{
			Responses: responses,
			Errors:    errs,
		}, nil
	}

	// 设置默认的合并和拆分函数
	if opts.BatchRequestFunc == nil {
		opts.BatchRequestFunc = func(requests []interface{}) interface{} {
			return requests
		}
	}

	if opts.SplitResponseFunc == nil {
		opts.SplitResponseFunc = func(batchResp interface{}, count int) []interface{} {
			result, ok := batchResp.(struct {
				Responses []interface{}
				Errors    []error
			})
			if !ok {
				return make([]interface{}, count)
			}
			return result.Responses
		}
	}

	// 创建批处理器
	return NewAdaptiveBatcher(batchProcessor, opts)
}
