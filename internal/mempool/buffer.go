package mempool

import (
	"bytes"
	"io"
	"sync"
)

// PooledBuffer 是一个来自缓冲区池的缓冲区，提供了buffer.Buffer的所有功能
// 优化了序列化/反序列化过程的内存分配
type PooledBuffer struct {
	*bytes.Buffer
	pool *AdvancedBufferPool
}

// AdvancedBufferPool 是一个增强的字节缓冲区池
// 与基本的BufferPool相比，添加了更多便捷方法和性能优化
type AdvancedBufferPool struct {
	pool    sync.Pool
	maxSize int
}

// NewAdvancedBufferPool 创建一个新的高级缓冲区池
func NewAdvancedBufferPool(initialSize, maxSize int) *AdvancedBufferPool {
	return &AdvancedBufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				buf := bytes.NewBuffer(make([]byte, 0, initialSize))
				return &PooledBuffer{
					Buffer: buf,
				}
			},
		},
		maxSize: maxSize,
	}
}

// Get 从池中获取一个缓冲区
func (p *AdvancedBufferPool) Get() *PooledBuffer {
	buf := p.pool.Get().(*PooledBuffer)
	buf.Reset() // 确保缓冲区为空
	buf.pool = p
	return buf
}

// Put 将缓冲区放回池中
func (p *AdvancedBufferPool) Put(buf *PooledBuffer) {
	// 如果缓冲区太大，不放回池中以避免内存泄漏
	if buf.Cap() > p.maxSize {
		return // 让GC回收
	}

	// 清空缓冲区以便重用，但保留底层存储
	buf.Reset()
	p.pool.Put(buf)
}

// Release 从PooledBuffer内部释放缓冲区
func (b *PooledBuffer) Release() {
	if b.pool != nil {
		b.pool.Put(b)
	}
}

// ReadFrom 实现io.ReaderFrom接口，优化从Reader复制数据到缓冲区
func (b *PooledBuffer) ReadFrom(r io.Reader) (n int64, err error) {
	// 一次性读取小批量数据
	var buf [4096]byte
	for {
		read, err := r.Read(buf[:])
		n += int64(read)
		if read > 0 {
			b.Write(buf[:read])
		}
		if err != nil {
			if err == io.EOF {
				return n, nil // EOF不是错误
			}
			return n, err
		}
	}
}

// WriteTo 实现io.WriterTo接口，优化从缓冲区复制数据到Writer
func (b *PooledBuffer) WriteTo(w io.Writer) (n int64, err error) {
	// 如果可能，使用批量写入
	if b.Len() == 0 {
		return 0, nil
	}

	m, err := w.Write(b.Bytes())
	return int64(m), err
}

// ProtoBufferPool 是专门为协议缓冲区优化的池
// 用于高效处理Protocol Buffers序列化/反序列化
type ProtoBufferPool struct {
	pool sync.Pool
	size int
}

// NewProtoBufferPool 创建一个新的Protocol Buffers缓冲区池
func NewProtoBufferPool(size int) *ProtoBufferPool {
	return &ProtoBufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				return &PooledBuffer{
					Buffer: bytes.NewBuffer(make([]byte, 0, size)),
				}
			},
		},
		size: size,
	}
}

// Get 从池中获取一个Proto缓冲区
func (p *ProtoBufferPool) Get() *PooledBuffer {
	buf := p.pool.Get().(*PooledBuffer)
	buf.Reset()
	return buf
}

// Put 将Proto缓冲区放回池中
func (p *ProtoBufferPool) Put(buf *PooledBuffer) {
	buf.Reset()
	p.pool.Put(buf)
}

// DefaultAdvancedBufferPool 创建默认大小的高级缓冲区池
var DefaultAdvancedBufferPool = NewAdvancedBufferPool(4096, 1024*1024)

// GetBuffer 从默认池获取缓冲区
func GetBuffer() *PooledBuffer {
	return DefaultAdvancedBufferPool.Get()
}

// PutBuffer 将缓冲区放回默认池
func PutBuffer(buf *PooledBuffer) {
	DefaultAdvancedBufferPool.Put(buf)
}

// PreallocatedByteSlicePool 预分配固定大小的字节切片池
// 用于避免频繁分配临时切片
type PreallocatedByteSlicePool struct {
	pool      sync.Pool
	sliceSize int
}

// NewPreallocatedByteSlicePool 创建一个新的预分配字节切片池
func NewPreallocatedByteSlicePool(sliceSize int) *PreallocatedByteSlicePool {
	return &PreallocatedByteSlicePool{
		pool: sync.Pool{
			New: func() interface{} {
				return make([]byte, sliceSize)
			},
		},
		sliceSize: sliceSize,
	}
}

// Get 从池中获取一个预分配的字节切片
func (p *PreallocatedByteSlicePool) Get() []byte {
	return p.pool.Get().([]byte)
}

// Put 将字节切片放回池中
func (p *PreallocatedByteSlicePool) Put(b []byte) {
	if len(b) != p.sliceSize {
		return // 大小不匹配，不放入池中
	}
	p.pool.Put(b)
}

// Common pools for frequent use cases
var (
	// Small4KPool 小型缓冲区池，适用于一般的小型JSON/Proto消息
	Small4KPool = NewAdvancedBufferPool(4096, 16384)

	// Medium64KPool 中型缓冲区池，适用于中等大小的消息
	Medium64KPool = NewAdvancedBufferPool(65536, 262144)

	// Large1MPool 大型缓冲区池，适用于大型消息
	Large1MPool = NewAdvancedBufferPool(1048576, 4194304)

	// TransferBufferPool 用于网络传输的4K缓冲区池
	TransferBufferPool = NewPreallocatedByteSlicePool(4096)
)
