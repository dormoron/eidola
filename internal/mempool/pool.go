package mempool

import (
	"sync"
)

// Pool 是一个通用的对象池，用于减少频繁创建和销毁对象带来的GC压力
type Pool[T any] struct {
	// 池中的对象
	pool sync.Pool
	// 对象创建函数
	new func() T
}

// New 创建一个新的对象池
// new 是一个创建新对象的函数
func New[T any](new func() T) *Pool[T] {
	return &Pool[T]{
		pool: sync.Pool{
			New: func() interface{} {
				return new()
			},
		},
		new: new,
	}
}

// Get 从池中获取一个对象，如果池中没有可用对象，则创建一个新的
func (p *Pool[T]) Get() T {
	return p.pool.Get().(T)
}

// Put 将对象放回池中，以便后续重用
func (p *Pool[T]) Put(obj T) {
	p.pool.Put(obj)
}

// BatchGet 从池中批量获取指定数量的对象
func (p *Pool[T]) BatchGet(count int) []T {
	result := make([]T, count)
	for i := 0; i < count; i++ {
		result[i] = p.Get()
	}
	return result
}

// BatchPut 批量将对象放回池中
func (p *Pool[T]) BatchPut(objs []T) {
	for _, obj := range objs {
		p.Put(obj)
	}
}

// SlicePool 是一个用于管理切片的特殊对象池
// 相比于直接使用Pool[[]T]，SlicePool可以更好地管理切片的容量
type SlicePool[T any] struct {
	// 池中的对象
	pool sync.Pool
	// 切片的初始容量
	capacity int
}

// NewSlicePool 创建一个新的切片池
// capacity 是切片的初始容量
func NewSlicePool[T any](capacity int) *SlicePool[T] {
	return &SlicePool[T]{
		pool: sync.Pool{
			New: func() interface{} {
				return make([]T, 0, capacity)
			},
		},
		capacity: capacity,
	}
}

// Get 从池中获取一个切片，切片长度为0但容量不小于初始设置
func (p *SlicePool[T]) Get() []T {
	slice := p.pool.Get().([]T)
	return slice[:0] // 返回长度为0的切片，但保留容量
}

// Put 将切片放回池中
// 注意：调用者应该确保不再持有对该切片的引用，因为切片可能被后续的Get操作获取并修改
func (p *SlicePool[T]) Put(slice []T) {
	// 检查切片的容量是否足够大，如果太小则不放入池中
	if cap(slice) >= p.capacity {
		p.pool.Put(slice[:0]) // 放回长度为0的切片，但保留容量
	}
	// 如果容量太小，就让它被GC回收
}

// GetWithSize 从池中获取一个切片，并设置其长度为size
// 所有元素都被初始化为零值
func (p *SlicePool[T]) GetWithSize(size int) []T {
	slice := p.Get()
	if cap(slice) < size {
		// 如果容量不足，创建一个新的
		slice = make([]T, size)
	} else {
		// 调整切片长度
		slice = slice[:size]
		// 清除旧值
		var zero T
		for i := range slice {
			slice[i] = zero
		}
	}
	return slice
}

// MapPool 是一个用于管理map的特殊对象池
type MapPool[K comparable, V any] struct {
	// 池中的对象
	pool sync.Pool
	// map的初始容量
	capacity int
}

// NewMapPool 创建一个新的map池
// capacity 是map的初始容量
func NewMapPool[K comparable, V any](capacity int) *MapPool[K, V] {
	return &MapPool[K, V]{
		pool: sync.Pool{
			New: func() interface{} {
				return make(map[K]V, capacity)
			},
		},
		capacity: capacity,
	}
}

// Get 从池中获取一个map
func (p *MapPool[K, V]) Get() map[K]V {
	m := p.pool.Get().(map[K]V)
	// 清空map
	for k := range m {
		delete(m, k)
	}
	return m
}

// Put 将map放回池中
func (p *MapPool[K, V]) Put(m map[K]V) {
	// 检查map的大小，如果太大则不放入池中（避免内存泄漏）
	if len(m) > p.capacity*2 {
		// 不放入池中，让GC回收
		return
	}
	p.pool.Put(m)
}

// BufferPool 是一个字节缓冲区池，专门优化用于处理字节数组
type BufferPool struct {
	// 池中的对象
	pool sync.Pool
	// 缓冲区的初始容量
	capacity int
}

// NewBufferPool 创建一个新的字节缓冲区池
// capacity 是缓冲区的初始容量
func NewBufferPool(capacity int) *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, capacity)
			},
		},
		capacity: capacity,
	}
}

// Get 从池中获取一个字节缓冲区
func (p *BufferPool) Get() []byte {
	buf := p.pool.Get().([]byte)
	return buf[:0] // 返回长度为0的缓冲区，但保留容量
}

// Put 将字节缓冲区放回池中
func (p *BufferPool) Put(buf []byte) {
	// 检查缓冲区的容量，如果太大则不放入池中
	if cap(buf) <= p.capacity*4 {
		p.pool.Put(buf[:0]) // 放回长度为0的缓冲区，但保留容量
	}
	// 如果容量太大，就让它被GC回收
}

// GetWithSize 从池中获取一个指定大小的字节缓冲区
func (p *BufferPool) GetWithSize(size int) []byte {
	buf := p.Get()
	if cap(buf) < size {
		// 如果容量不足，创建一个新的
		return make([]byte, size)
	}
	// 调整切片长度
	return buf[:size]
}
