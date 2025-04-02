package stream

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BackpressureOptions 背压控制选项
type BackpressureOptions struct {
	// 最大缓冲区大小（消息数量）
	MaxBufferSize int
	// 高水位线百分比，当缓冲区使用率超过此值时开始限流
	HighWatermarkPct float64
	// 低水位线百分比，当缓冲区使用率低于此值时恢复正常处理
	LowWatermarkPct float64
	// 缓冲区满时的等待超时
	WaitTimeout time.Duration
	// 背压生效时的节流因子 (0-1)，表示放行的概率
	ThrottleFactor float64
	// 是否在缓冲区满时阻塞而不是返回错误
	BlockOnFull bool
}

// DefaultBackpressureOptions 返回默认的背压选项
func DefaultBackpressureOptions() BackpressureOptions {
	return BackpressureOptions{
		MaxBufferSize:    1000,
		HighWatermarkPct: 0.8, // 80%
		LowWatermarkPct:  0.5, // 50%
		WaitTimeout:      5 * time.Second,
		ThrottleFactor:   0.5,  // 50%概率放行
		BlockOnFull:      true, // 默认阻塞
	}
}

// BackpressureInterceptor 流式RPC的背压控制拦截器
type BackpressureInterceptor struct {
	opts          BackpressureOptions
	currentLoads  map[string]int
	activeStreams map[string]int
	mu            sync.RWMutex
}

// NewBackpressureInterceptor 创建一个新的背压控制拦截器
func NewBackpressureInterceptor(opts BackpressureOptions) *BackpressureInterceptor {
	return &BackpressureInterceptor{
		opts:          opts,
		currentLoads:  make(map[string]int),
		activeStreams: make(map[string]int),
	}
}

// StreamServerInterceptor 返回服务端流拦截器
func (b *BackpressureInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 包装流，添加背压控制
		wrappedStream := &backpressureServerStream{
			ServerStream: ss,
			interceptor:  b,
			method:       info.FullMethod,
		}

		// 增加活跃流计数
		b.mu.Lock()
		b.activeStreams[info.FullMethod]++
		b.mu.Unlock()

		// 确保在流处理结束时减少计数
		defer func() {
			b.mu.Lock()
			b.activeStreams[info.FullMethod]--
			// 清理不再有活跃流的方法
			if b.activeStreams[info.FullMethod] <= 0 {
				delete(b.activeStreams, info.FullMethod)
				delete(b.currentLoads, info.FullMethod)
			}
			b.mu.Unlock()
		}()

		// 调用实际的处理器
		return handler(srv, wrappedStream)
	}
}

// backpressureServerStream 是带背压控制的ServerStream包装器
type backpressureServerStream struct {
	grpc.ServerStream
	interceptor *BackpressureInterceptor
	method      string
	sendLock    sync.Mutex
}

// SendMsg 重写发送消息方法，添加背压控制
func (s *backpressureServerStream) SendMsg(m interface{}) error {
	// 确保同一时间只有一个goroutine在发送
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	// 获取当前负载
	s.interceptor.mu.RLock()
	currentLoad := s.interceptor.currentLoads[s.method]
	maxBuffer := s.interceptor.opts.MaxBufferSize
	s.interceptor.mu.RUnlock()

	// 检查是否需要应用背压
	if currentLoad >= int(float64(maxBuffer)*s.interceptor.opts.HighWatermarkPct) {
		// 已经达到高水位线
		if currentLoad >= maxBuffer {
			// 缓冲区已满
			if s.interceptor.opts.BlockOnFull {
				// 阻塞模式：等待缓冲区空间
				ctx, cancel := context.WithTimeout(context.Background(), s.interceptor.opts.WaitTimeout)
				defer cancel()

				// 等待直到缓冲区低于高水位线或超时
				for {
					select {
					case <-time.After(10 * time.Millisecond): // 轮询间隔
						s.interceptor.mu.RLock()
						currentLoad = s.interceptor.currentLoads[s.method]
						s.interceptor.mu.RUnlock()

						if currentLoad < int(float64(maxBuffer)*s.interceptor.opts.HighWatermarkPct) {
							// 负载已下降，可以继续发送
							break
						}
					case <-ctx.Done():
						// 等待超时
						return status.Errorf(codes.ResourceExhausted, "stream backpressure buffer full, timeout waiting for buffer space")
					}
				}
			} else {
				// 非阻塞模式：直接返回错误
				return status.Errorf(codes.ResourceExhausted, "stream backpressure buffer full")
			}
		} else {
			// 在高水位线和最大缓冲区之间，使用节流因子
			// 随机决定是否处理此消息
			if shouldThrottle(s.interceptor.opts.ThrottleFactor) {
				// 节流，延迟处理
				time.Sleep(10 * time.Millisecond)
			}
		}
	}

	// 增加负载计数
	s.interceptor.mu.Lock()
	s.interceptor.currentLoads[s.method]++
	s.interceptor.mu.Unlock()

	// 发送消息
	err := s.ServerStream.SendMsg(m)

	// 减少负载计数
	s.interceptor.mu.Lock()
	s.interceptor.currentLoads[s.method]--
	s.interceptor.mu.Unlock()

	return err
}

// RecvMsg 重写接收消息方法，更新背压状态
func (s *backpressureServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)

	// 如果是EOF或其他错误，不更新状态
	if err != nil {
		return err
	}

	// 更新负载（这里只是记录接收状态，实际的背压控制在SendMsg中应用）
	s.interceptor.mu.Lock()
	currentLoad := s.interceptor.currentLoads[s.method]
	lowWatermark := int(float64(s.interceptor.opts.MaxBufferSize) * s.interceptor.opts.LowWatermarkPct)

	// 检查是否已经降到低水位线以下
	if currentLoad <= lowWatermark {
		// 可以考虑在这里增加一些优化，例如触发更积极的处理
	}
	s.interceptor.mu.Unlock()

	return nil
}

// 随机决定是否应用节流，基于给定的节流因子
func shouldThrottle(factor float64) bool {
	// 简单实现：使用时间的纳秒部分作为随机源
	// 在生产环境中，应该使用更好的随机源
	return float64(time.Now().Nanosecond()%1000)/1000.0 > factor
}

// StreamClientInterceptor 返回客户端流拦截器
func (b *BackpressureInterceptor) StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// 获取原始客户端流
		clientStream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			return nil, err
		}

		// 包装为背压感知的客户端流
		return &backpressureClientStream{
			ClientStream: clientStream,
			interceptor:  b,
			method:       method,
		}, nil
	}
}

// backpressureClientStream 是带背压控制的ClientStream包装器
type backpressureClientStream struct {
	grpc.ClientStream
	interceptor *BackpressureInterceptor
	method      string
	sendLock    sync.Mutex
}

// SendMsg 重写发送消息方法，添加背压控制
func (s *backpressureClientStream) SendMsg(m interface{}) error {
	// 客户端流发送逻辑与服务端类似
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	s.interceptor.mu.RLock()
	currentLoad := s.interceptor.currentLoads[s.method]
	maxBuffer := s.interceptor.opts.MaxBufferSize
	s.interceptor.mu.RUnlock()

	// 检查是否需要应用背压
	if currentLoad >= int(float64(maxBuffer)*s.interceptor.opts.HighWatermarkPct) {
		// 已经达到高水位线，与服务端逻辑相同
		if currentLoad >= maxBuffer {
			if s.interceptor.opts.BlockOnFull {
				// 阻塞等待
				ctx, cancel := context.WithTimeout(context.Background(), s.interceptor.opts.WaitTimeout)
				defer cancel()

				for {
					select {
					case <-time.After(10 * time.Millisecond):
						s.interceptor.mu.RLock()
						currentLoad = s.interceptor.currentLoads[s.method]
						s.interceptor.mu.RUnlock()

						if currentLoad < int(float64(maxBuffer)*s.interceptor.opts.HighWatermarkPct) {
							break
						}
					case <-ctx.Done():
						return status.Errorf(codes.ResourceExhausted, "client stream backpressure buffer full, timeout waiting for buffer space")
					}
				}
			} else {
				return status.Errorf(codes.ResourceExhausted, "client stream backpressure buffer full")
			}
		} else if shouldThrottle(s.interceptor.opts.ThrottleFactor) {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// 增加负载计数
	s.interceptor.mu.Lock()
	s.interceptor.currentLoads[s.method]++
	s.interceptor.mu.Unlock()

	// 发送消息
	err := s.ClientStream.SendMsg(m)

	// 减少负载计数
	s.interceptor.mu.Lock()
	s.interceptor.currentLoads[s.method]--
	s.interceptor.mu.Unlock()

	return err
}

// RecvMsg 重写接收消息方法
func (s *backpressureClientStream) RecvMsg(m interface{}) error {
	return s.ClientStream.RecvMsg(m)
}
