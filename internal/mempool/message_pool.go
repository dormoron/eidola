package mempool

import (
	"reflect"
	"runtime"
	"sync"
	"time"
)

// MessagePoolOptions 消息池配置选项
type MessagePoolOptions struct {
	// 最大池大小，超过此大小的对象会被回收
	MaxPoolSize int
	// 清理周期，多久清理一次过期对象
	CleanupInterval time.Duration
	// 对象过期时间
	TTL time.Duration
	// 预热大小，池初始化时预创建的对象数量
	PrewarmSize int
	// 采用引用计数机制
	UseRefCounting bool
	// 在获取对象后运行重置函数
	ResetOnGet bool
}

// DefaultMessagePoolOptions 返回默认的消息池配置选项
func DefaultMessagePoolOptions() MessagePoolOptions {
	return MessagePoolOptions{
		MaxPoolSize:     10000,
		CleanupInterval: 5 * time.Minute,
		TTL:             10 * time.Minute,
		PrewarmSize:     10,
		UseRefCounting:  true,
		ResetOnGet:      true,
	}
}

// MessagePool 是一个泛型消息对象池，适用于Protocol Buffers消息等
type MessagePool struct {
	factory     func() interface{}
	reset       func(obj interface{})
	pool        sync.Pool
	objects     map[interface{}]*pooledObject
	maxPoolSize int
	ttl         time.Duration
	useRefCount bool
	resetOnGet  bool
	mu          sync.RWMutex
	stopCh      chan struct{}
	wg          sync.WaitGroup
	stats       MessagePoolStats
}

// pooledObject 表示池中的对象及其元数据
type pooledObject struct {
	object    interface{}
	createdAt time.Time
	lastUsed  time.Time
	refCount  int32
}

// MessagePoolStats 消息池统计信息
type MessagePoolStats struct {
	Created         uint64
	Reused          uint64
	Destroyed       uint64
	ObjectsInPool   int
	MaxSize         int
	TotalAllocation uint64
}

// NewMessagePool 创建一个新的消息对象池
func NewMessagePool(factory func() interface{}, reset func(interface{}), options MessagePoolOptions) *MessagePool {
	p := &MessagePool{
		factory:     factory,
		reset:       reset,
		objects:     make(map[interface{}]*pooledObject),
		maxPoolSize: options.MaxPoolSize,
		ttl:         options.TTL,
		useRefCount: options.UseRefCounting,
		resetOnGet:  options.ResetOnGet,
		stopCh:      make(chan struct{}),
		stats: MessagePoolStats{
			MaxSize: options.MaxPoolSize,
		},
	}

	p.pool = sync.Pool{
		New: func() interface{} {
			obj := factory()
			p.mu.Lock()
			p.stats.Created++
			p.stats.TotalAllocation++
			p.objects[obj] = &pooledObject{
				object:    obj,
				createdAt: time.Now(),
				lastUsed:  time.Now(),
				refCount:  1,
			}
			p.mu.Unlock()
			return obj
		},
	}

	// 预热池
	if options.PrewarmSize > 0 {
		for i := 0; i < options.PrewarmSize; i++ {
			obj := p.pool.New()
			p.Put(obj)
		}
	}

	// 启动清理协程
	if options.CleanupInterval > 0 {
		p.wg.Add(1)
		go p.cleanupLoop(options.CleanupInterval)
	}

	return p
}

// Get 从池中获取一个对象
func (p *MessagePool) Get() interface{} {
	obj := p.pool.Get()

	if p.resetOnGet && p.reset != nil {
		p.reset(obj)
	}

	if p.useRefCount {
		p.mu.Lock()
		if po, exists := p.objects[obj]; exists {
			po.lastUsed = time.Now()
			po.refCount++
			p.stats.Reused++
		}
		p.mu.Unlock()
	}

	return obj
}

// Put 将对象放回池中
func (p *MessagePool) Put(obj interface{}) {
	if obj == nil {
		return
	}

	p.mu.Lock()
	poolSize := len(p.objects)
	po, exists := p.objects[obj]

	if p.useRefCount && exists {
		// 如果使用引用计数且对象存在
		po.refCount--
		// 如果引用计数为0，可以放回池中或销毁
		if po.refCount <= 0 {
			if poolSize >= p.maxPoolSize {
				// 池已满，直接销毁对象
				delete(p.objects, obj)
				p.stats.Destroyed++
				p.mu.Unlock()
				return
			}
			// 更新对象的最后使用时间
			po.lastUsed = time.Now()
			p.pool.Put(obj)
		}
	} else {
		// 不使用引用计数或对象不存在
		if poolSize >= p.maxPoolSize {
			// 池已满，直接销毁对象
			p.stats.Destroyed++
			p.mu.Unlock()
			return
		}
		// 放回池中
		if !exists {
			p.objects[obj] = &pooledObject{
				object:    obj,
				createdAt: time.Now(),
				lastUsed:  time.Now(),
			}
		} else {
			po.lastUsed = time.Now()
		}
		p.pool.Put(obj)
	}

	p.stats.ObjectsInPool = len(p.objects)
	p.mu.Unlock()
}

// cleanupLoop 清理过期对象的后台协程
func (p *MessagePool) cleanupLoop(interval time.Duration) {
	defer p.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.cleanup()
		}
	}
}

// cleanup 清理过期对象
func (p *MessagePool) cleanup() {
	now := time.Now()
	toRemove := make([]interface{}, 0)

	p.mu.Lock()
	for key, po := range p.objects {
		// 如果对象过期且没有被引用，则移除
		if p.ttl > 0 && now.Sub(po.lastUsed) > p.ttl &&
			(!p.useRefCount || po.refCount <= 0) {
			toRemove = append(toRemove, key)
		}
	}

	for _, key := range toRemove {
		delete(p.objects, key)
		p.stats.Destroyed++
	}

	p.stats.ObjectsInPool = len(p.objects)
	p.mu.Unlock()

	// 触发GC
	if len(toRemove) > 100 {
		runtime.GC()
	}
}

// GetStats 获取池统计信息
func (p *MessagePool) GetStats() MessagePoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stats
}

// Close 关闭池并释放资源
func (p *MessagePool) Close() {
	close(p.stopCh)
	p.wg.Wait()

	p.mu.Lock()
	p.objects = make(map[interface{}]*pooledObject)
	p.mu.Unlock()
}

// NewTypedMessagePool 创建一个类型安全的消息对象池
func NewTypedMessagePool[T any](options MessagePoolOptions) *TypedMessagePool[T] {
	var zeroValue T
	factory := func() interface{} {
		// 使用反射创建T类型的新实例
		val := reflect.New(reflect.TypeOf(zeroValue)).Interface()
		// 如果T是指针类型，则取得其指向的值
		if reflect.TypeOf(zeroValue).Kind() == reflect.Ptr {
			return val
		}
		// 否则返回指针指向的值
		return reflect.ValueOf(val).Elem().Interface()
	}

	reset := func(obj interface{}) {
		// 对于Protocol Buffers消息，可以调用Reset方法
		if resetter, ok := obj.(interface{ Reset() }); ok {
			resetter.Reset()
		}
	}

	pool := NewMessagePool(factory, reset, options)
	return &TypedMessagePool[T]{pool: pool}
}

// TypedMessagePool 提供类型安全的消息对象池
type TypedMessagePool[T any] struct {
	pool *MessagePool
}

// Get 获取指定类型的对象
func (p *TypedMessagePool[T]) Get() T {
	return p.pool.Get().(T)
}

// Put 归还对象到池中
func (p *TypedMessagePool[T]) Put(obj T) {
	p.pool.Put(obj)
}

// GetStats 获取池统计信息
func (p *TypedMessagePool[T]) GetStats() MessagePoolStats {
	return p.pool.GetStats()
}

// Close 关闭池
func (p *TypedMessagePool[T]) Close() {
	p.pool.Close()
}
