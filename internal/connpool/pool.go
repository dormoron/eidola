package connpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc"
)

var (
	ErrPoolClosed = errors.New("connection pool is closed")
	ErrTimeout    = errors.New("connection pool: get connection timed out")
)

// Options 配置连接池参数
type Options struct {
	// 最大空闲连接数
	MaxIdle int
	// 最大活跃连接数
	MaxActive int
	// 连接最大空闲时间，超过这个时间将被关闭
	IdleTimeout time.Duration
	// 获取连接的最大等待时间
	WaitTimeout time.Duration
	// 是否在连接池满时阻塞等待
	Wait bool
}

// ConnectionFactory 用于创建新的连接
type ConnectionFactory func(ctx context.Context, target string) (*grpc.ClientConn, error)

// Pool 连接池接口
type Pool interface {
	// Get 获取一个连接
	Get(ctx context.Context) (*PoolConn, error)
	// Put 归还一个连接
	Put(conn *PoolConn) error
	// Close 关闭连接池
	Close() error
	// Status 返回连接池状态信息
	Status() Status
}

// Status 连接池状态
type Status struct {
	// 活跃连接数
	ActiveCount int
	// 空闲连接数
	IdleCount int
	// 等待获取连接的请求数
	WaitCount int64
	// 连接池最大连接数
	MaxActive int
}

// PoolConn 包装grpc.ClientConn，添加连接池管理相关信息
type PoolConn struct {
	*grpc.ClientConn
	pool     *connPool
	lastUsed time.Time
	closed   bool
	mu       sync.RWMutex
}

// Close 关闭连接或将连接归还到池中
func (pc *PoolConn) Close() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.closed {
		return nil
	}

	pc.closed = true
	return pc.pool.Put(pc)
}

// connPool 连接池实现
type connPool struct {
	// 连接池配置
	opts Options
	// 空闲连接列表
	idle []*PoolConn
	// 活跃连接数
	active int
	// 等待获取连接的请求数
	waitCount int64
	// 用于创建新连接的工厂函数
	factory ConnectionFactory
	// 连接目标地址
	target string
	// 互斥锁保护并发访问
	mu sync.Mutex
	// 连接池是否已关闭
	closed bool
	// 用于等待可用连接的条件变量
	cond *sync.Cond
	// 清理空闲连接的定时器
	cleanerCh chan struct{}
}

// NewPool 创建新的连接池
func NewPool(target string, factory ConnectionFactory, opts Options) Pool {
	p := &connPool{
		opts:      opts,
		factory:   factory,
		target:    target,
		idle:      make([]*PoolConn, 0, opts.MaxIdle),
		cleanerCh: make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)

	// 启动空闲连接清理器
	if opts.IdleTimeout > 0 {
		go p.cleaner()
	}

	return p
}

// Get 从连接池获取一个连接
func (p *connPool) Get(ctx context.Context) (*PoolConn, error) {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	// 首先尝试复用空闲连接
	if conn := p.getIdleConn(); conn != nil {
		p.active++
		p.mu.Unlock()
		return conn, nil
	}

	// 如果活跃连接数小于最大限制，创建新连接
	if p.opts.MaxActive == 0 || p.active < p.opts.MaxActive {
		p.active++
		p.mu.Unlock()

		conn, err := p.factory(ctx, p.target)
		if err != nil {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
			return nil, err
		}

		pc := &PoolConn{
			ClientConn: conn,
			pool:       p,
			lastUsed:   time.Now(),
		}
		return pc, nil
	}

	// 如果不等待，则直接返回错误
	if !p.opts.Wait {
		p.mu.Unlock()
		return nil, errors.New("connection pool exhausted")
	}

	// 等待可用连接
	p.waitCount++
	waitDone := make(chan struct{})

	// 监听上下文取消和等待超时
	var timeoutCh <-chan time.Time
	if p.opts.WaitTimeout > 0 {
		timer := time.NewTimer(p.opts.WaitTimeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	ready := false

	// 启动goroutine监听条件变量
	go func() {
		p.cond.Wait()
		close(waitDone)
	}()

	p.mu.Unlock()

	// 等待可用连接或超时/上下文取消
	select {
	case <-waitDone:
		ready = true
	case <-timeoutCh:
		// 超时
		p.mu.Lock()
		p.waitCount--
		p.mu.Unlock()
		return nil, ErrTimeout
	case <-ctx.Done():
		// 上下文取消
		p.mu.Lock()
		p.waitCount--
		p.mu.Unlock()
		return nil, ctx.Err()
	}

	if !ready {
		return nil, errors.New("wait for connection failed")
	}

	// 再次尝试获取连接
	p.mu.Lock()
	p.waitCount--

	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	if conn := p.getIdleConn(); conn != nil {
		p.active++
		p.mu.Unlock()
		return conn, nil
	}

	if p.opts.MaxActive == 0 || p.active < p.opts.MaxActive {
		p.active++
		p.mu.Unlock()

		conn, err := p.factory(ctx, p.target)
		if err != nil {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
			return nil, err
		}

		pc := &PoolConn{
			ClientConn: conn,
			pool:       p,
			lastUsed:   time.Now(),
		}
		return pc, nil
	}

	p.mu.Unlock()
	return nil, errors.New("failed to get connection from pool")
}

// getIdleConn 获取空闲连接，不加锁，调用方需负责加锁
func (p *connPool) getIdleConn() *PoolConn {
	n := len(p.idle)
	if n == 0 {
		return nil
	}

	// 获取最后一个空闲连接
	conn := p.idle[n-1]
	p.idle = p.idle[:n-1]

	// 检查连接是否过期
	if p.opts.IdleTimeout > 0 && conn.lastUsed.Add(p.opts.IdleTimeout).Before(time.Now()) {
		// 关闭过期连接
		conn.ClientConn.Close()
		return nil
	}

	return conn
}

// Put 归还连接到连接池
func (p *connPool) Put(conn *PoolConn) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		// 如果连接池已关闭，直接关闭连接
		return conn.ClientConn.Close()
	}

	p.active--

	// 更新最后使用时间
	conn.lastUsed = time.Now()
	conn.closed = false

	// 如果有等待获取连接的请求，通知一个
	if p.waitCount > 0 {
		p.cond.Signal()
		return nil
	}

	// 如果空闲连接数小于最大限制，则放入空闲连接池
	if p.opts.MaxIdle > 0 && len(p.idle) < p.opts.MaxIdle {
		p.idle = append(p.idle, conn)
		return nil
	}

	// 否则关闭连接
	return conn.ClientConn.Close()
}

// Close 关闭连接池
func (p *connPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true

	// 关闭所有空闲连接
	for _, conn := range p.idle {
		conn.ClientConn.Close()
	}

	p.idle = nil

	// 广播通知所有等待的goroutine
	p.cond.Broadcast()

	// 停止清理器
	if p.cleanerCh != nil {
		close(p.cleanerCh)
	}

	return nil
}

// Status 返回连接池状态
func (p *connPool) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()

	return Status{
		ActiveCount: p.active,
		IdleCount:   len(p.idle),
		WaitCount:   p.waitCount,
		MaxActive:   p.opts.MaxActive,
	}
}

// cleaner 定期清理过期的空闲连接
func (p *connPool) cleaner() {
	ticker := time.NewTicker(p.opts.IdleTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				return
			}

			// 检查并关闭过期连接
			if p.opts.IdleTimeout > 0 {
				expiredTime := time.Now().Add(-p.opts.IdleTimeout)
				var valid []*PoolConn

				for _, conn := range p.idle {
					if conn.lastUsed.After(expiredTime) {
						valid = append(valid, conn)
					} else {
						conn.ClientConn.Close()
					}
				}

				p.idle = valid
			}
			p.mu.Unlock()
		case <-p.cleanerCh:
			return
		}
	}
}
