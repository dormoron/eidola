package compression

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"

	"google.golang.org/grpc/encoding"
)

// 定义错误
var (
	ErrNotFound = errors.New("encoding not found")
)

// Register all codecs that we support
func init() {
	encoding.RegisterCompressor(&AdaptiveCompressor{
		name:                "adaptive",
		underlyingAlgorithm: "gzip",
		thresholdBytes:      1024, // 默认1KB以上的消息才压缩
	})
}

// AdaptiveCompressor 是一个智能压缩器，根据消息大小自动决定是否启用压缩
type AdaptiveCompressor struct {
	name                string  // 压缩器名称
	underlyingAlgorithm string  // 底层压缩算法
	thresholdBytes      int     // 压缩阈值（字节数）
	minRatio            float64 // 最小压缩比，低于此值不进行压缩
	mu                  sync.RWMutex
}

// Name 返回压缩器名称
func (c *AdaptiveCompressor) Name() string {
	return c.name
}

// Compress 根据消息大小自适应压缩
func (c *AdaptiveCompressor) Compress(w io.Writer) (io.WriteCloser, error) {
	c.mu.RLock()
	threshold := c.thresholdBytes
	algorithm := c.underlyingAlgorithm
	c.mu.RUnlock()

	// 创建一个自适应写入器，它会缓存初始的一部分数据来决定是否需要压缩
	return &adaptiveWriter{
		w:              w,
		threshold:      threshold,
		algorithm:      algorithm,
		buffer:         make([]byte, 0, threshold+4), // 额外的4字节用于存储原始大小
		compressor:     nil,
		shouldCompress: true, // 默认压缩
	}, nil
}

// SetThreshold 设置压缩阈值
func (c *AdaptiveCompressor) SetThreshold(bytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thresholdBytes = bytes
}

// SetUnderlyingAlgorithm 设置底层压缩算法
func (c *AdaptiveCompressor) SetUnderlyingAlgorithm(algorithm string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.underlyingAlgorithm = algorithm
}

// SetMinRatio 设置最小压缩比
func (c *AdaptiveCompressor) SetMinRatio(ratio float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.minRatio = ratio
}

// Decompress 实现解压缩
func (c *AdaptiveCompressor) Decompress(r io.Reader) (io.Reader, error) {
	// 读取前4个字节以确定是否实际进行了压缩
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	// 判断是否压缩 (0表示未压缩，1表示已压缩)
	isCompressed := header[0] == 1

	if !isCompressed {
		// 如果未压缩，直接返回原始数据
		originalSize := binary.BigEndian.Uint32(header[0:4])
		return io.LimitReader(r, int64(originalSize)), nil
	}

	// 如果压缩了，使用底层算法解压缩
	c.mu.RLock()
	algorithm := c.underlyingAlgorithm
	c.mu.RUnlock()

	compressor := encoding.GetCompressor(algorithm)
	if compressor == nil {
		return nil, ErrNotFound
	}
	return compressor.Decompress(r)
}

// 自适应写入器
type adaptiveWriter struct {
	w              io.Writer
	threshold      int
	algorithm      string
	buffer         []byte
	compressor     io.WriteCloser
	shouldCompress bool
	compressed     bool
}

// Write 实现io.Writer接口
func (aw *adaptiveWriter) Write(p []byte) (int, error) {
	if aw.compressor != nil {
		// 如果压缩器已经创建，使用压缩器写入
		return aw.compressor.Write(p)
	}

	// 将数据添加到缓冲区
	if len(aw.buffer)+len(p) < aw.threshold {
		// 缓冲区未满，继续缓存
		aw.buffer = append(aw.buffer, p...)
		return len(p), nil
	}

	// 缓冲区已满，决定是否压缩
	if aw.shouldCompress {
		// 初始化压缩器
		compressor := encoding.GetCompressor(aw.algorithm)
		if compressor == nil {
			return 0, ErrNotFound
		}

		// 写入压缩标记
		header := []byte{1, 0, 0, 0} // 1表示使用压缩
		if _, err := aw.w.Write(header); err != nil {
			return 0, err
		}

		var err error
		aw.compressor, err = compressor.Compress(aw.w)
		if err != nil {
			return 0, err
		}

		// 将缓冲区的数据写入压缩器
		if _, err := aw.compressor.Write(aw.buffer); err != nil {
			return 0, err
		}
		aw.compressed = true

		// 写入当前数据块
		return aw.compressor.Write(p)
	} else {
		// 不压缩
		// 写入未压缩标记和长度
		// 计算未压缩长度（缓冲区 + 当前数据）
		totalSize := uint32(len(aw.buffer) + len(p))
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, totalSize)
		header[0] = 0 // 覆盖第一个字节表示不压缩

		if _, err := aw.w.Write(header); err != nil {
			return 0, err
		}

		// 直接写入缓冲区数据和当前数据
		if _, err := aw.w.Write(aw.buffer); err != nil {
			return 0, err
		}
		n, err := aw.w.Write(p)
		if err != nil {
			return len(aw.buffer) + n, err
		}
		aw.compressor = &noOpWriteCloser{aw.w}
		return len(p), nil
	}
}

// Close 关闭写入器
func (aw *adaptiveWriter) Close() error {
	if aw.compressor != nil {
		// 如果压缩器已创建，关闭它
		return aw.compressor.Close()
	}

	// 压缩器未创建，直接输出缓冲区中的内容（不压缩）
	totalSize := uint32(len(aw.buffer))
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, totalSize)
	header[0] = 0 // 覆盖第一个字节表示不压缩

	if _, err := aw.w.Write(header); err != nil {
		return err
	}

	if _, err := aw.w.Write(aw.buffer); err != nil {
		return err
	}

	return nil
}

// 无操作的WriteCloser
type noOpWriteCloser struct {
	io.Writer
}

func (n *noOpWriteCloser) Close() error {
	if closer, ok := n.Writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// NewAdaptiveCompressor 创建一个新的自适应压缩器
func NewAdaptiveCompressor(algorithm string, thresholdBytes int, minRatio float64) *AdaptiveCompressor {
	if thresholdBytes <= 0 {
		thresholdBytes = 1024 // 默认1KB
	}
	if minRatio <= 0 || minRatio >= 1 {
		minRatio = 0.7 // 默认压缩比至少30%才启用压缩
	}

	ac := &AdaptiveCompressor{
		name:                "adaptive",
		underlyingAlgorithm: algorithm,
		thresholdBytes:      thresholdBytes,
		minRatio:            minRatio,
	}

	return ac
}

// DetectCompressionRatio 检测数据的可压缩性，返回预估的压缩比
// 使用采样方法，速度快但精度较低
func DetectCompressionRatio(data []byte) float64 {
	if len(data) == 0 {
		return 1.0
	}

	// 计算数据熵值作为压缩比的近似估计
	// 熵越高，数据越随机，压缩比越低
	entropy := calculateEntropy(data)

	// 熵值的范围是0-8（对于字节数据）
	// 将熵映射到压缩比：熵低时压缩效果好，熵高时压缩效果差
	ratio := entropy / 8.0

	// 避免返回太低的值
	if ratio < 0.1 {
		ratio = 0.1
	}

	return ratio
}

// calculateEntropy 计算字节数据的熵值
func calculateEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}

	// 计算每个字节的频率
	counts := make(map[byte]int)
	for _, b := range data {
		counts[b]++
	}

	// 计算熵
	var entropy float64
	dataLen := float64(len(data))
	for _, count := range counts {
		p := float64(count) / dataLen
		entropy -= p * math.Log2(p)
	}

	return entropy
}
