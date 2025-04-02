package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var (
	// 客户端一次性初始化并注册所有指标收集器
	once   sync.Once
	client *ClientMetrics
)

// ClientMetrics 包含所有客户端指标
type ClientMetrics struct {
	RequestDuration   *prometheus.HistogramVec // 请求耗时直方图
	RequestsTotal     *prometheus.CounterVec   // 请求总数计数器
	RequestsInFlight  *prometheus.GaugeVec     // 进行中的请求数量
	RequestsFailures  *prometheus.CounterVec   // 失败请求计数器
	ConnectionsTotal  *prometheus.CounterVec   // 连接总数
	ConnectionsActive *prometheus.GaugeVec     // 活跃连接数
	StreamsActive     *prometheus.GaugeVec     // 活跃流数量
	StreamMessages    *prometheus.CounterVec   // 流消息计数
	RetryAttempts     *prometheus.CounterVec   // 重试次数
	RequestSizes      *prometheus.HistogramVec // 请求大小直方图
	ResponseSizes     *prometheus.HistogramVec // 响应大小直方图
	CircuitBreakers   *prometheus.GaugeVec     // 断路器状态
}

// ClientMetricsOptions 配置客户端指标
type ClientMetricsOptions struct {
	Namespace           string            // 指标命名空间
	Subsystem           string            // 指标子系统
	ConstLabels         prometheus.Labels // 常量标签
	DurationBuckets     []float64         // 耗时直方图桶
	RequestSizeBuckets  []float64         // 请求大小直方图桶
	ResponseSizeBuckets []float64         // 响应大小直方图桶
}

// DefaultClientMetricsOptions 返回默认客户端指标配置
func DefaultClientMetricsOptions() ClientMetricsOptions {
	return ClientMetricsOptions{
		Namespace:   "grpc",
		Subsystem:   "client",
		ConstLabels: prometheus.Labels{},
		DurationBuckets: []float64{
			0.001, // 1ms
			0.005, // 5ms
			0.01,  // 10ms
			0.025, // 25ms
			0.05,  // 50ms
			0.1,   // 100ms
			0.25,  // 250ms
			0.5,   // 500ms
			1,     // 1s
			2.5,   // 2.5s
			5,     // 5s
			10,    // 10s
		},
		RequestSizeBuckets: []float64{
			128,     // 128B
			256,     // 256B
			512,     // 512B
			1024,    // 1KB
			4096,    // 4KB
			16384,   // 16KB
			65536,   // 64KB
			262144,  // 256KB
			1048576, // 1MB
		},
		ResponseSizeBuckets: []float64{
			128,     // 128B
			256,     // 256B
			512,     // 512B
			1024,    // 1KB
			4096,    // 4KB
			16384,   // 16KB
			65536,   // 64KB
			262144,  // 256KB
			1048576, // 1MB
			4194304, // 4MB
		},
	}
}

// GetClientMetrics 返回客户端指标单例实例
func GetClientMetrics(opts ClientMetricsOptions) *ClientMetrics {
	once.Do(func() {
		client = createClientMetrics(opts)
	})
	return client
}

// createClientMetrics 创建并注册所有客户端指标
func createClientMetrics(opts ClientMetricsOptions) *ClientMetrics {
	metrics := &ClientMetrics{
		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "request_duration_seconds",
				Help:        "gRPC客户端请求耗时(秒)",
				ConstLabels: opts.ConstLabels,
				Buckets:     opts.DurationBuckets,
			},
			[]string{"method", "service", "status"},
		),
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "requests_total",
				Help:        "gRPC客户端请求总数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"method", "service"},
		),
		RequestsInFlight: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "requests_in_flight",
				Help:        "gRPC客户端当前进行中的请求数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"method", "service"},
		),
		RequestsFailures: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "request_failures_total",
				Help:        "gRPC客户端请求失败总数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"method", "service", "status"},
		),
		ConnectionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "connections_total",
				Help:        "gRPC客户端连接总数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"target"},
		),
		ConnectionsActive: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "connections_active",
				Help:        "gRPC客户端当前活跃连接数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"target"},
		),
		StreamsActive: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "streams_active",
				Help:        "gRPC客户端当前活跃流数量",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"method", "service"},
		),
		StreamMessages: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "stream_messages_total",
				Help:        "gRPC客户端流消息总数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"method", "service", "direction"},
		),
		RetryAttempts: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "retry_attempts_total",
				Help:        "gRPC客户端重试总次数",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"method", "service"},
		),
		RequestSizes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "request_size_bytes",
				Help:        "gRPC客户端请求大小(字节)",
				ConstLabels: opts.ConstLabels,
				Buckets:     opts.RequestSizeBuckets,
			},
			[]string{"method", "service"},
		),
		ResponseSizes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "response_size_bytes",
				Help:        "gRPC客户端响应大小(字节)",
				ConstLabels: opts.ConstLabels,
				Buckets:     opts.ResponseSizeBuckets,
			},
			[]string{"method", "service"},
		),
		CircuitBreakers: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   opts.Namespace,
				Subsystem:   opts.Subsystem,
				Name:        "circuit_breaker_state",
				Help:        "gRPC客户端断路器状态(0=关闭, 1=半开, 2=开路)",
				ConstLabels: opts.ConstLabels,
			},
			[]string{"target", "circuit_name"},
		),
	}

	return metrics
}

// UnaryClientInterceptor 返回一个带指标收集的一元客户端拦截器
func (m *ClientMetrics) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 解析服务名和方法名
		service, method := splitMethodName(method)

		// 增加请求计数
		m.RequestsTotal.WithLabelValues(method, service).Inc()

		// 增加进行中请求计数
		m.RequestsInFlight.WithLabelValues(method, service).Inc()
		defer m.RequestsInFlight.WithLabelValues(method, service).Dec()

		// 如果请求可测量大小，记录请求大小
		if sizer, ok := req.(interface{ Size() int }); ok {
			m.RequestSizes.WithLabelValues(method, service).Observe(float64(sizer.Size()))
		}

		// 记录请求耗时
		startTime := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)
		duration := time.Since(startTime).Seconds()

		// 设置状态码
		statusCode := "OK"
		if err != nil {
			if s, ok := status.FromError(err); ok {
				statusCode = s.Code().String()
			} else {
				statusCode = "UNKNOWN"
			}

			// 增加失败计数
			m.RequestsFailures.WithLabelValues(method, service, statusCode).Inc()
		}

		// 记录请求耗时
		m.RequestDuration.WithLabelValues(method, service, statusCode).Observe(duration)

		// 如果响应可测量大小，记录响应大小
		if sizer, ok := reply.(interface{ Size() int }); ok {
			m.ResponseSizes.WithLabelValues(method, service).Observe(float64(sizer.Size()))
		}

		return err
	}
}

// StreamClientInterceptor 返回一个带指标收集的流式客户端拦截器
func (m *ClientMetrics) StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// 解析服务名和方法名
		service, method := splitMethodName(method)

		// 增加请求计数
		m.RequestsTotal.WithLabelValues(method, service).Inc()

		// 记录请求耗时
		startTime := time.Now()
		clientStream, err := streamer(ctx, desc, cc, method, opts...)

		// 设置状态码
		statusCode := "OK"
		if err != nil {
			if s, ok := status.FromError(err); ok {
				statusCode = s.Code().String()
			} else {
				statusCode = "UNKNOWN"
			}

			// 增加失败计数
			m.RequestsFailures.WithLabelValues(method, service, statusCode).Inc()

			// 记录请求耗时
			duration := time.Since(startTime).Seconds()
			m.RequestDuration.WithLabelValues(method, service, statusCode).Observe(duration)

			return nil, err
		}

		// 增加活跃流计数
		m.StreamsActive.WithLabelValues(method, service).Inc()

		// 包装客户端流来收集指标
		return &monitoredClientStream{
			ClientStream: clientStream,
			method:       method,
			service:      service,
			metrics:      m,
			startTime:    startTime,
		}, nil
	}
}

// monitoredClientStream 包装 grpc.ClientStream 来收集流指标
type monitoredClientStream struct {
	grpc.ClientStream
	method    string
	service   string
	metrics   *ClientMetrics
	startTime time.Time
	finished  bool
	mutex     sync.Mutex
}

func (s *monitoredClientStream) SendMsg(m interface{}) error {
	// 如果消息可测量大小，记录大小
	if sizer, ok := m.(interface{ Size() int }); ok {
		s.metrics.RequestSizes.WithLabelValues(s.method, s.service).Observe(float64(sizer.Size()))
	}

	err := s.ClientStream.SendMsg(m)

	if err == nil {
		// 增加发送消息计数
		s.metrics.StreamMessages.WithLabelValues(s.method, s.service, "sent").Inc()
	}

	return err
}

func (s *monitoredClientStream) RecvMsg(m interface{}) error {
	err := s.ClientStream.RecvMsg(m)

	if err == nil {
		// 增加接收消息计数
		s.metrics.StreamMessages.WithLabelValues(s.method, s.service, "received").Inc()

		// 如果消息可测量大小，记录大小
		if sizer, ok := m.(interface{ Size() int }); ok {
			s.metrics.ResponseSizes.WithLabelValues(s.method, s.service).Observe(float64(sizer.Size()))
		}
	}

	// 检查流是否结束
	if err != nil {
		s.mutex.Lock()
		defer s.mutex.Unlock()

		if !s.finished {
			s.finished = true

			// 减少活跃流计数
			s.metrics.StreamsActive.WithLabelValues(s.method, s.service).Dec()

			// 记录流持续时间
			duration := time.Since(s.startTime).Seconds()

			// 设置状态码
			statusCode := "OK"
			if s, ok := status.FromError(err); ok {
				statusCode = s.Code().String()
			} else {
				statusCode = "UNKNOWN"
			}

			// 记录请求耗时
			s.metrics.RequestDuration.WithLabelValues(s.method, s.service, statusCode).Observe(duration)

			// 如果不是EOF，增加失败计数
			if statusCode != "OK" {
				s.metrics.RequestsFailures.WithLabelValues(s.method, s.service, statusCode).Inc()
			}
		}
	}

	return err
}

func (s *monitoredClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if !s.finished {
		s.finished = true

		// 减少活跃流计数
		s.metrics.StreamsActive.WithLabelValues(s.method, s.service).Dec()

		// 记录流持续时间
		duration := time.Since(s.startTime).Seconds()

		// 设置状态码
		statusCode := "OK"
		if err != nil {
			if st, ok := status.FromError(err); ok {
				statusCode = st.Code().String()
			} else {
				statusCode = "UNKNOWN"
			}
		}

		// 记录请求耗时
		s.metrics.RequestDuration.WithLabelValues(s.method, s.service, statusCode).Observe(duration)
	}

	return err
}

// 解析完整方法名为服务名和方法名
// 输入格式: /package.service/method
func splitMethodName(fullMethodName string) (string, string) {
	fullMethodName = fullMethodName[1:] // 去掉前导 "/"

	// 查找最后一个 "/"
	pos := 0
	for i := 0; i < len(fullMethodName); i++ {
		if fullMethodName[i] == '/' {
			pos = i
		}
	}

	service := fullMethodName[:pos]
	method := fullMethodName[pos+1:]

	return service, method
}

// IncrementConnectionsMetrics 增加连接计数
func (m *ClientMetrics) IncrementConnectionsMetrics(target string) {
	m.ConnectionsTotal.WithLabelValues(target).Inc()
	m.ConnectionsActive.WithLabelValues(target).Inc()
}

// DecrementConnectionsMetrics 减少连接计数
func (m *ClientMetrics) DecrementConnectionsMetrics(target string) {
	m.ConnectionsActive.WithLabelValues(target).Dec()
}

// SetCircuitBreakerState 设置断路器状态
func (m *ClientMetrics) SetCircuitBreakerState(target, name string, state int) {
	m.CircuitBreakers.WithLabelValues(target, name).Set(float64(state))
}

// IncrementRetryMetrics 增加重试计数
func (m *ClientMetrics) IncrementRetryMetrics(fullMethod string) {
	service, method := splitMethodName(fullMethod)
	m.RetryAttempts.WithLabelValues(method, service).Inc()
}
