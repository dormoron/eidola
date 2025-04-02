package batch

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Options 配置批处理器参数
type Options struct {
	// 批次大小，达到此数量时将触发批处理
	BatchSize int
	// 最大等待时间，即使未达到BatchSize，经过此时间也会触发处理
	MaxLatency time.Duration
	// 工作器数量，即处理批次的并发数
	Workers int
	// 是否阻塞等待批处理完成
	WaitForBatch bool
	// 批处理队列的缓冲大小
	QueueSize int
	// 单个请求的默认超时时间
	DefaultTimeout time.Duration
}

// DefaultOptions 返回默认的批处理器配置
func DefaultOptions() Options {
	return Options{
		BatchSize:      100,
		MaxLatency:     100 * time.Millisecond,
		Workers:        5,
		WaitForBatch:   false,
		QueueSize:      1000,
		DefaultTimeout: 5 * time.Second,
	}
}

// Processor 是一个通用的批量处理器
type Processor[T any, R any] struct {
	opts    Options
	process func(context.Context, []T) ([]R, []error)
	items   []T
	results []R
	errors  []error
	indices []int
	reqChan chan *request[T, R]
	quit    chan struct{}
	mu      sync.Mutex
	timer   *time.Timer
	wg      sync.WaitGroup
}

// request 表示一个批处理请求
type request[T any, R any] struct {
	item     T
	index    int
	respChan chan response[R]
	ctx      context.Context
	result   R
	err      error
}

// response 表示一个批处理响应
type response[R any] struct {
	result R
	err    error
}

// NewProcessor 创建一个新的批处理器
func NewProcessor[T any, R any](
	process func(context.Context, []T) ([]R, []error),
	opts Options,
) *Processor[T, R] {
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultOptions().BatchSize
	}
	if opts.MaxLatency <= 0 {
		opts.MaxLatency = DefaultOptions().MaxLatency
	}
	if opts.Workers <= 0 {
		opts.Workers = DefaultOptions().Workers
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = DefaultOptions().QueueSize
	}
	if opts.DefaultTimeout <= 0 {
		opts.DefaultTimeout = DefaultOptions().DefaultTimeout
	}

	p := &Processor[T, R]{
		opts:    opts,
		process: process,
		reqChan: make(chan *request[T, R], opts.QueueSize),
		quit:    make(chan struct{}),
		items:   make([]T, 0, opts.BatchSize),
		results: make([]R, 0, opts.BatchSize),
		errors:  make([]error, 0, opts.BatchSize),
		indices: make([]int, 0, opts.BatchSize),
		timer:   time.NewTimer(opts.MaxLatency),
	}

	// 确保计时器开始时停止，只在必要时启动
	if !p.timer.Stop() {
		<-p.timer.C
	}

	// 启动工作器
	p.wg.Add(opts.Workers)
	for i := 0; i < opts.Workers; i++ {
		go p.worker()
	}

	return p
}

// Process 处理单个请求，如果达到批次大小会立即处理
func (p *Processor[T, R]) Process(ctx context.Context, item T) (R, error) {
	// 如果没有设置上下文超时，添加默认超时
	if _, ok := ctx.Deadline(); !ok && p.opts.DefaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.opts.DefaultTimeout)
		defer cancel()
	}

	// 创建响应通道
	respChan := make(chan response[R], 1)
	req := &request[T, R]{
		item:     item,
		respChan: respChan,
		ctx:      ctx,
	}

	// 发送请求到队列
	select {
	case p.reqChan <- req:
		// 成功加入队列
	case <-ctx.Done():
		// 上下文取消或超时
		var zero R
		return zero, ctx.Err()
	case <-p.quit:
		// 处理器已关闭
		var zero R
		return zero, errors.New("processor is closed")
	}

	// 如果不需要等待批处理完成，则返回一个预设的响应
	if !p.opts.WaitForBatch {
		var zero R
		return zero, nil
	}

	// 等待响应
	select {
	case resp := <-respChan:
		return resp.result, resp.err
	case <-ctx.Done():
		// 上下文取消或超时
		var zero R
		return zero, ctx.Err()
	case <-p.quit:
		// 处理器已关闭
		var zero R
		return zero, errors.New("processor is closed")
	}
}

// worker 工作器负责从队列接收请求并批量处理
func (p *Processor[T, R]) worker() {
	defer p.wg.Done()

	for {
		select {
		case req := <-p.reqChan:
			// 处理请求
			p.handleRequest(req)
		case <-p.quit:
			// 处理器关闭
			return
		}
	}
}

// handleRequest 处理单个请求并可能触发批处理
func (p *Processor[T, R]) handleRequest(req *request[T, R]) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 检查上下文是否已取消
	if req.ctx.Err() != nil {
		if p.opts.WaitForBatch {
			var zero R
			req.respChan <- response[R]{zero, req.ctx.Err()}
		}
		return
	}

	// 将请求添加到批次中
	p.items = append(p.items, req.item)
	p.indices = append(p.indices, len(p.items)-1)

	// 如果是批次中的第一个项目，启动定时器
	if len(p.items) == 1 {
		p.timer.Reset(p.opts.MaxLatency)
	}

	// 如果达到批次大小或定时器到期，处理批次
	if len(p.items) >= p.opts.BatchSize {
		p.processBatch()
	}
}

// processBatch 处理当前批次
func (p *Processor[T, R]) processBatch() {
	// 如果没有项目，不处理
	if len(p.items) == 0 {
		return
	}

	// 停止定时器
	if !p.timer.Stop() {
		select {
		case <-p.timer.C:
		default:
		}
	}

	// 创建一个合并的上下文
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 处理批次
	results, errs := p.process(ctx, p.items)

	// 确保结果和错误数组长度匹配
	if len(results) != len(p.items) || len(errs) != len(p.items) {
		// 处理长度不匹配的情况
		if len(results) < len(p.items) {
			// 扩展结果数组
			var zero R
			for i := len(results); i < len(p.items); i++ {
				results = append(results, zero)
			}
		}
		if len(errs) < len(p.items) {
			// 扩展错误数组
			for i := len(errs); i < len(p.items); i++ {
				errs = append(errs, errors.New("batch processing error: missing result"))
			}
		}
	}

	// 如果需要等待批处理完成，发送响应
	if p.opts.WaitForBatch {
		for i, idx := range p.indices {
			select {
			case p.reqChan <- &request[T, R]{
				index:    idx,
				respChan: make(chan response[R], 1),
				result:   results[i],
				err:      errs[i],
			}:
			default:
				// 队列已满，忽略
			}
		}
	}

	// 清除当前批次
	p.items = p.items[:0]
	p.indices = p.indices[:0]
}

// Close 关闭批处理器
func (p *Processor[T, R]) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 如果定时器仍在运行，处理最后一批
	if len(p.items) > 0 {
		p.processBatch()
	}

	// 关闭退出通道，停止所有工作器
	close(p.quit)

	// 等待所有工作器退出
	p.wg.Wait()

	return nil
}
