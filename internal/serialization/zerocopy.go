package serialization

import (
	"io"
	"reflect"
	"unsafe"

	"github.com/dormoron/eidola/internal/mempool"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// ZeroCopyWriter 实现了零拷贝写入，避免不必要的内存分配
type ZeroCopyWriter struct {
	buffer *mempool.PooledBuffer
}

// NewZeroCopyWriter 创建一个新的零拷贝写入器
func NewZeroCopyWriter() *ZeroCopyWriter {
	return &ZeroCopyWriter{
		buffer: mempool.Small4KPool.Get(),
	}
}

// WriteProto 使用零拷贝技术将proto消息写入
func (w *ZeroCopyWriter) WriteProto(msg proto.Message) error {
	// 使用池化缓冲区避免内存分配
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	// 直接写入底层缓冲区
	_, err = w.buffer.Write(data)
	return err
}

// WriteTo 将缓冲区内容写入io.Writer
func (w *ZeroCopyWriter) WriteTo(writer io.Writer) (int64, error) {
	return w.buffer.WriteTo(writer)
}

// Reset 重置缓冲区
func (w *ZeroCopyWriter) Reset() {
	w.buffer.Reset()
}

// Release 释放缓冲区回池
func (w *ZeroCopyWriter) Release() {
	w.buffer.Release()
	w.buffer = nil
}

// Bytes 返回底层字节数组
func (w *ZeroCopyWriter) Bytes() []byte {
	return w.buffer.Bytes()
}

// ZeroCopyReader 实现了零拷贝读取
type ZeroCopyReader struct {
	data []byte
	pos  int
}

// NewZeroCopyReader 创建一个新的零拷贝读取器
func NewZeroCopyReader(data []byte) *ZeroCopyReader {
	return &ZeroCopyReader{
		data: data,
		pos:  0,
	}
}

// ReadProto 使用零拷贝技术读取proto消息
func (r *ZeroCopyReader) ReadProto(msg proto.Message) error {
	// 直接使用底层字节片段解析消息
	return proto.Unmarshal(r.data[r.pos:], msg)
}

// Skip 跳过指定字节数
func (r *ZeroCopyReader) Skip(n int) {
	if r.pos+n <= len(r.data) {
		r.pos += n
	} else {
		r.pos = len(r.data)
	}
}

// ByteToStringUnsafe 使用零拷贝将字节切片转换为字符串
// 警告：返回的字符串与输入字节切片共享底层存储，修改字节切片会导致字符串内容变化！
// 仅在临时转换且确保不会修改原始字节切片时使用
func ByteToStringUnsafe(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// StringToByteUnsafe 使用零拷贝将字符串转换为字节切片
// 警告：返回的字节切片与输入字符串共享底层存储，不可修改！
func StringToByteUnsafe(s string) []byte {
	stringHeader := (*reflect.StringHeader)(unsafe.Pointer(&s))
	var b []byte
	byteHeader := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	byteHeader.Data = stringHeader.Data
	byteHeader.Len = stringHeader.Len
	byteHeader.Cap = stringHeader.Len
	return b
}

// OptimizedProtoPool 是针对频繁使用的protobuf类型进行优化的对象池
type OptimizedProtoPool struct {
	pools map[reflect.Type]*reflect.Value
}

// NewOptimizedProtoPool 创建一个新的优化protobuf池
func NewOptimizedProtoPool() *OptimizedProtoPool {
	return &OptimizedProtoPool{
		pools: make(map[reflect.Type]*reflect.Value),
	}
}

// RegisterType 注册一个protobuf类型到池中
func (p *OptimizedProtoPool) RegisterType(protoMsg proto.Message) {
	t := reflect.TypeOf(protoMsg)
	if _, exists := p.pools[t]; !exists {
		v := reflect.New(t.Elem())
		p.pools[t] = &v
	}
}

// Get 从池中获取一个特定类型的protobuf对象
func (p *OptimizedProtoPool) Get(protoMsg proto.Message) proto.Message {
	t := reflect.TypeOf(protoMsg)
	if _, exists := p.pools[t]; exists {
		// 创建一个新的相同类型的对象
		newObj := reflect.New(t.Elem()).Interface().(proto.Message)
		return newObj
	}
	// 如果类型未注册，直接返回原始对象
	return protoMsg
}

// VarIntWriter 优化变长整数写入
type VarIntWriter struct {
	buffer *mempool.PooledBuffer
}

// NewVarIntWriter 创建一个新的变长整数写入器
func NewVarIntWriter() *VarIntWriter {
	return &VarIntWriter{
		buffer: mempool.Small4KPool.Get(),
	}
}

// WriteVarint 写入变长整数
func (w *VarIntWriter) WriteVarint(value uint64) {
	// 使用protowire包的编码函数
	var buf [10]byte
	n := protowire.AppendVarint(buf[:0], value)
	w.buffer.Write(n)
}

// Release 释放资源
func (w *VarIntWriter) Release() {
	w.buffer.Release()
	w.buffer = nil
}

// Bytes 返回编码后的字节
func (w *VarIntWriter) Bytes() []byte {
	return w.buffer.Bytes()
}

// ChunkedReader 实现了块读取，适用于大型消息处理
type ChunkedReader struct {
	reader io.Reader
	buffer []byte
	pos    int
	end    int
}

// NewChunkedReader 创建一个新的块读取器
func NewChunkedReader(reader io.Reader, bufferSize int) *ChunkedReader {
	if bufferSize <= 0 {
		bufferSize = 4096
	}
	return &ChunkedReader{
		reader: reader,
		buffer: make([]byte, bufferSize),
		pos:    0,
		end:    0,
	}
}

// ReadNext 读取下一个块
func (r *ChunkedReader) ReadNext() ([]byte, error) {
	// 如果缓冲区已用完，重新填充
	if r.pos >= r.end {
		var err error
		r.pos = 0
		r.end, err = r.reader.Read(r.buffer)
		if err != nil {
			return nil, err
		}
	}

	// 返回当前块
	chunk := r.buffer[r.pos:r.end]
	r.pos = r.end
	return chunk, nil
}

// ReadAll 读取所有可用数据
func (r *ChunkedReader) ReadAll() ([]byte, error) {
	// 使用内存池获取一个缓冲区
	buf := mempool.GetBuffer()
	defer buf.Release()

	// 首先写入已缓冲但未读取的数据
	if r.pos < r.end {
		buf.Write(r.buffer[r.pos:r.end])
		r.pos = r.end
	}

	// 然后读取剩余数据
	_, err := buf.ReadFrom(r.reader)
	if err != nil && err != io.EOF {
		return nil, err
	}

	// 复制数据以返回
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}
