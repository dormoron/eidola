package metrics

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MethodMetricsOptions 方法级指标选项
type MethodMetricsOptions struct {
	// 是否启用每个方法的详细指标
	EnableMethodLevelMetrics bool
	// 应用名称
	AppName string
	// 服务名称
	ServiceName string
	// gRPC响应码至HTTP状态码的映射函数
	GRPCCodeToHTTPCode func(code codes.Code) int
	// 是否收集响应时间分位数
	EnableLatencyHistogram bool
	// 响应时间分位数边界（单位：毫秒）
	LatencyBuckets []float64
	// 是否收集请求/响应大小指标
	EnableSizeMetrics bool
	// 大小指标边界（单位：字节）
	SizeBuckets []float64
	// 是否收集速率指标（例如每秒请求数）
	EnableRateMetrics bool
	// 是否收集并发指标
	EnableConcurrencyMetrics bool
	// 是否收集回调统计信息（例如，重试次数）
	EnableCallbackMetrics bool
	// 是否启用标签化的指标（便于细粒度查询）
	EnableLabeledMetrics bool
	// 标签定义
	Labels map[string]string
	// 指标前缀
	MetricsPrefix string
	// 指标注册表，如为nil则使用默认注册表
	Registry prometheus.Registerer
}

// DefaultMethodMetricsOptions 返回默认的方法级指标选项
func DefaultMethodMetricsOptions() MethodMetricsOptions {
	return MethodMetricsOptions{
		EnableMethodLevelMetrics: true,
		AppName:                  "app",
		ServiceName:              "service",
		GRPCCodeToHTTPCode:       defaultGRPCCodeToHTTPCode,
		EnableLatencyHistogram:   true,
		LatencyBuckets:           []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
		EnableSizeMetrics:        true,
		SizeBuckets:              []float64{128, 512, 1024, 4096, 16384, 65536, 262144, 1048576},
		EnableRateMetrics:        true,
		EnableConcurrencyMetrics: true,
		EnableCallbackMetrics:    true,
		EnableLabeledMetrics:     true,
		Labels:                   map[string]string{},
		MetricsPrefix:            "grpc",
		Registry:                 prometheus.DefaultRegisterer,
	}
}

// defaultGRPCCodeToHTTPCode 默认的gRPC状态码到HTTP状态码的映射
func defaultGRPCCodeToHTTPCode(code codes.Code) int {
	switch code {
	case codes.OK:
		return 200
	case codes.Canceled:
		return 499
	case codes.Unknown:
		return 500
	case codes.InvalidArgument:
		return 400
	case codes.DeadlineExceeded:
		return 504
	case codes.NotFound:
		return 404
	case codes.AlreadyExists:
		return 409
	case codes.PermissionDenied:
		return 403
	case codes.ResourceExhausted:
		return 429
	case codes.FailedPrecondition:
		return 400
	case codes.Aborted:
		return 409
	case codes.OutOfRange:
		return 400
	case codes.Unimplemented:
		return 501
	case codes.Internal:
		return 500
	case codes.Unavailable:
		return 503
	case codes.DataLoss:
		return 500
	case codes.Unauthenticated:
		return 401
	default:
		return 500
	}
}

// MethodMetrics 方法级指标收集器
type MethodMetrics struct {
	opts                  MethodMetricsOptions
	requestCounter        *prometheus.CounterVec
	responseCounter       *prometheus.CounterVec
	latencyHistogram      *prometheus.HistogramVec
	requestSizeHistogram  *prometheus.HistogramVec
	responseSizeHistogram *prometheus.HistogramVec
	inFlightGauge         *prometheus.GaugeVec
	callbackCounter       *prometheus.CounterVec
	rateGauge             *prometheus.GaugeVec
	errorCounter          *prometheus.CounterVec
	panicCounter          *prometheus.CounterVec
	retryCounter          *prometheus.CounterVec

	// 时间窗口指标
	timeWindowMetrics map[string]*timeWindowCounter
	timeWindowMu      sync.RWMutex
}

// timeWindowCounter 时间窗口计数器，用于计算短时间内的速率
type timeWindowCounter struct {
	window     time.Duration
	buckets    []float64
	timestamps []time.Time
	index      int
	mu         sync.Mutex
}

// NewTimeWindowCounter 创建新的时间窗口计数器
func newTimeWindowCounter(window time.Duration, bucketCount int) *timeWindowCounter {
	return &timeWindowCounter{
		window:     window,
		buckets:    make([]float64, bucketCount),
		timestamps: make([]time.Time, bucketCount),
		index:      0,
	}
}

// Add 向时间窗口计数器添加值
func (t *timeWindowCounter) Add(value float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buckets[t.index] = value
	t.timestamps[t.index] = time.Now()
	t.index = (t.index + 1) % len(t.buckets)
}

// Rate 计算时间窗口内的速率
func (t *timeWindowCounter) Rate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-t.window)

	var sum float64
	var count int
	for i, ts := range t.timestamps {
		if !ts.IsZero() && ts.After(windowStart) {
			sum += t.buckets[i]
			count++
		}
	}

	if count == 0 {
		return 0
	}

	// 返回单位时间内的速率
	return sum / t.window.Seconds()
}

// NewMethodMetrics 创建新的方法级指标收集器
func NewMethodMetrics(opts MethodMetricsOptions) *MethodMetrics {
	prefix := opts.MetricsPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix = prefix + "_"
	}

	constLabels := prometheus.Labels{}
	for k, v := range opts.Labels {
		constLabels[k] = v
	}

	if opts.AppName != "" {
		constLabels["app"] = opts.AppName
	}

	if opts.ServiceName != "" {
		constLabels["service"] = opts.ServiceName
	}

	m := &MethodMetrics{
		opts: opts,
		requestCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        prefix + "request_total",
				Help:        "Total number of gRPC requests received",
				ConstLabels: constLabels,
			},
			[]string{"method", "type"},
		),
		responseCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        prefix + "response_total",
				Help:        "Total number of gRPC responses sent",
				ConstLabels: constLabels,
			},
			[]string{"method", "code", "type", "http_code"},
		),
		errorCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        prefix + "errors_total",
				Help:        "Total number of gRPC errors",
				ConstLabels: constLabels,
			},
			[]string{"method", "type", "error_type"},
		),
		panicCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        prefix + "panics_total",
				Help:        "Total number of gRPC panics recovered",
				ConstLabels: constLabels,
			},
			[]string{"method", "type"},
		),
		retryCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        prefix + "retries_total",
				Help:        "Total number of gRPC retries",
				ConstLabels: constLabels,
			},
			[]string{"method", "type"},
		),
		timeWindowMetrics: make(map[string]*timeWindowCounter),
	}

	// 添加可选指标
	if opts.EnableLatencyHistogram {
		m.latencyHistogram = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        prefix + "latency_milliseconds",
				Help:        "Latency of gRPC requests in milliseconds",
				ConstLabels: constLabels,
				Buckets:     opts.LatencyBuckets,
			},
			[]string{"method", "type", "code"},
		)
	}

	if opts.EnableSizeMetrics {
		m.requestSizeHistogram = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        prefix + "request_size_bytes",
				Help:        "Size of gRPC requests in bytes",
				ConstLabels: constLabels,
				Buckets:     opts.SizeBuckets,
			},
			[]string{"method", "type"},
		)

		m.responseSizeHistogram = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        prefix + "response_size_bytes",
				Help:        "Size of gRPC responses in bytes",
				ConstLabels: constLabels,
				Buckets:     opts.SizeBuckets,
			},
			[]string{"method", "type", "code"},
		)
	}

	if opts.EnableConcurrencyMetrics {
		m.inFlightGauge = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name:        prefix + "in_flight",
				Help:        "Number of in-flight gRPC requests",
				ConstLabels: constLabels,
			},
			[]string{"method", "type"},
		)
	}

	if opts.EnableCallbackMetrics {
		m.callbackCounter = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        prefix + "callback_total",
				Help:        "Total number of gRPC callbacks",
				ConstLabels: constLabels,
			},
			[]string{"method", "type", "event"},
		)
	}

	if opts.EnableRateMetrics {
		m.rateGauge = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name:        prefix + "rate",
				Help:        "Rate of gRPC requests per second",
				ConstLabels: constLabels,
			},
			[]string{"method", "type", "window"},
		)
	}

	// 注册指标
	registry := opts.Registry
	if registry == nil {
		registry = prometheus.DefaultRegisterer
	}

	registry.MustRegister(
		m.requestCounter,
		m.responseCounter,
		m.errorCounter,
		m.panicCounter,
		m.retryCounter,
	)

	if m.latencyHistogram != nil {
		registry.MustRegister(m.latencyHistogram)
	}

	if m.requestSizeHistogram != nil {
		registry.MustRegister(m.requestSizeHistogram)
	}

	if m.responseSizeHistogram != nil {
		registry.MustRegister(m.responseSizeHistogram)
	}

	if m.inFlightGauge != nil {
		registry.MustRegister(m.inFlightGauge)
	}

	if m.callbackCounter != nil {
		registry.MustRegister(m.callbackCounter)
	}

	if m.rateGauge != nil {
		registry.MustRegister(m.rateGauge)

		// 启动速率计算协程
		go m.updateRates()
	}

	return m
}

// updateRates 更新速率指标的后台协程
func (m *MethodMetrics) updateRates() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.timeWindowMu.RLock()
		for key, counter := range m.timeWindowMetrics {
			parts := strings.Split(key, ":")
			if len(parts) == 3 {
				method, typ, window := parts[0], parts[1], parts[2]
				m.rateGauge.WithLabelValues(method, typ, window).Set(counter.Rate())
			}
		}
		m.timeWindowMu.RUnlock()
	}
}

// extractMethodInfo 从完整方法名中提取服务和方法信息
func extractMethodInfo(fullMethod string) (string, string, string) {
	if len(fullMethod) == 0 {
		return "unknown", "unknown", "unknown"
	}

	// 标准gRPC方法格式: /package.service/method
	parts := strings.Split(fullMethod, "/")
	if len(parts) != 3 {
		return fullMethod, "unknown", "unknown"
	}

	serviceParts := strings.Split(parts[1], ".")
	service := serviceParts[len(serviceParts)-1]

	return fullMethod, service, parts[2]
}

// getTimeWindowCounter 获取或创建时间窗口计数器
func (m *MethodMetrics) getTimeWindowCounter(method, typ, window string, duration time.Duration, bucketCount int) *timeWindowCounter {
	key := method + ":" + typ + ":" + window

	m.timeWindowMu.RLock()
	counter, ok := m.timeWindowMetrics[key]
	m.timeWindowMu.RUnlock()

	if !ok {
		counter = newTimeWindowCounter(duration, bucketCount)

		m.timeWindowMu.Lock()
		m.timeWindowMetrics[key] = counter
		m.timeWindowMu.Unlock()
	}

	return counter
}

// RecordRequest 记录请求统计信息
func (m *MethodMetrics) RecordRequest(ctx context.Context, fullMethod string, requestSize int, typ string) context.Context {
	if !m.opts.EnableMethodLevelMetrics {
		return ctx
	}

	fullMethod, _, _ = extractMethodInfo(fullMethod)

	m.requestCounter.WithLabelValues(fullMethod, typ).Inc()

	if m.requestSizeHistogram != nil {
		m.requestSizeHistogram.WithLabelValues(fullMethod, typ).Observe(float64(requestSize))
	}

	if m.inFlightGauge != nil {
		m.inFlightGauge.WithLabelValues(fullMethod, typ).Inc()
	}

	// 记录1分钟窗口的请求速率
	if m.rateGauge != nil {
		counter := m.getTimeWindowCounter(fullMethod, typ, "1m", time.Minute, 60)
		counter.Add(1)
	}

	// 在上下文中保存请求开始时间
	return context.WithValue(ctx, contextKeyStartTime{}, time.Now())
}

// RecordResponse 记录响应统计信息
func (m *MethodMetrics) RecordResponse(ctx context.Context, fullMethod string, responseSize int, err error, typ string) {
	if !m.opts.EnableMethodLevelMetrics {
		return
	}

	fullMethod, _, _ = extractMethodInfo(fullMethod)

	// 获取gRPC状态码
	var code codes.Code
	if err != nil {
		if st, ok := status.FromError(err); ok {
			code = st.Code()
		} else {
			code = codes.Unknown
		}

		// 记录错误
		var errorType string
		if code == codes.DeadlineExceeded {
			errorType = "timeout"
		} else if code == codes.Canceled {
			errorType = "canceled"
		} else {
			errorType = "other"
		}

		m.errorCounter.WithLabelValues(fullMethod, typ, errorType).Inc()
	} else {
		code = codes.OK
	}

	httpCode := m.opts.GRPCCodeToHTTPCode(code)
	m.responseCounter.WithLabelValues(fullMethod, code.String(), typ, string(rune(httpCode))).Inc()

	// 减少在途请求计数
	if m.inFlightGauge != nil {
		m.inFlightGauge.WithLabelValues(fullMethod, typ).Dec()
	}

	// 记录响应大小
	if m.responseSizeHistogram != nil {
		m.responseSizeHistogram.WithLabelValues(fullMethod, typ, code.String()).Observe(float64(responseSize))
	}

	// 记录延迟时间
	if m.latencyHistogram != nil {
		if startTime, ok := ctx.Value(contextKeyStartTime{}).(time.Time); ok {
			elapsedMs := float64(time.Since(startTime)) / float64(time.Millisecond)
			m.latencyHistogram.WithLabelValues(fullMethod, typ, code.String()).Observe(elapsedMs)
		}
	}
}

// RecordPanic 记录恐慌(panic)统计信息
func (m *MethodMetrics) RecordPanic(fullMethod, typ string) {
	if !m.opts.EnableMethodLevelMetrics {
		return
	}

	fullMethod, _, _ = extractMethodInfo(fullMethod)
	m.panicCounter.WithLabelValues(fullMethod, typ).Inc()
}

// RecordRetry 记录重试统计信息
func (m *MethodMetrics) RecordRetry(fullMethod, typ string) {
	if !m.opts.EnableMethodLevelMetrics {
		return
	}

	fullMethod, _, _ = extractMethodInfo(fullMethod)
	m.retryCounter.WithLabelValues(fullMethod, typ).Inc()
}

// RecordCallback 记录回调事件
func (m *MethodMetrics) RecordCallback(fullMethod, typ, event string) {
	if !m.opts.EnableMethodLevelMetrics || m.callbackCounter == nil {
		return
	}

	fullMethod, _, _ = extractMethodInfo(fullMethod)
	m.callbackCounter.WithLabelValues(fullMethod, typ, event).Inc()
}

// 上下文键类型
type contextKeyStartTime struct{}

// UnaryServerInterceptor 返回一元服务器拦截器用于收集指标
func (m *MethodMetrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 估计请求大小
		requestSize := 0
		if m.opts.EnableSizeMetrics {
			// 实际使用时应该提供更精确的大小估计
			requestSize = 64 // 默认估计大小
		}

		// 记录请求统计信息
		ctx = m.RecordRequest(ctx, info.FullMethod, requestSize, "unary")

		// 设置恐慌(panic)恢复
		defer func() {
			if r := recover(); r != nil {
				// 记录恐慌
				m.RecordPanic(info.FullMethod, "unary")
				// 重新抛出恐慌
				panic(r)
			}
		}()

		// 处理请求
		resp, err := handler(ctx, req)

		// 估计响应大小
		responseSize := 0
		if m.opts.EnableSizeMetrics {
			// 实际使用时应该提供更精确的大小估计
			responseSize = 128 // 默认估计大小
		}

		// 记录响应统计信息
		m.RecordResponse(ctx, info.FullMethod, responseSize, err, "unary")

		return resp, err
	}
}

// StreamServerInterceptor 返回流服务器拦截器用于收集指标
func (m *MethodMetrics) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 包装流以记录每个消息
		wrappedStream := &metricsServerStream{
			ServerStream: ss,
			metrics:      m,
			fullMethod:   info.FullMethod,
			recvCount:    0,
			sendCount:    0,
		}

		// 记录请求开始
		ctx := m.RecordRequest(ss.Context(), info.FullMethod, 0, streamType(info))

		// 设置新上下文的流
		wrappedStream.ctx = ctx

		// 设置恐慌(panic)恢复
		defer func() {
			if r := recover(); r != nil {
				// 记录恐慌
				m.RecordPanic(info.FullMethod, streamType(info))
				// 重新抛出恐慌
				panic(r)
			}
		}()

		// 处理流
		err := handler(srv, wrappedStream)

		// 记录响应结束
		m.RecordResponse(ctx, info.FullMethod, 0, err, streamType(info))

		return err
	}
}

// streamType 返回流类型
func streamType(info *grpc.StreamServerInfo) string {
	if info.IsClientStream && info.IsServerStream {
		return "bidi_stream"
	} else if info.IsClientStream {
		return "client_stream"
	} else if info.IsServerStream {
		return "server_stream"
	}
	return "unknown_stream"
}

// metricsServerStream 包装的服务器流，用于记录每个消息
type metricsServerStream struct {
	grpc.ServerStream
	metrics    *MethodMetrics
	fullMethod string
	recvCount  int
	sendCount  int
	ctx        context.Context
}

// Context 返回带有指标的上下文
func (s *metricsServerStream) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return s.ServerStream.Context()
}

// SendMsg 发送消息并记录大小
func (s *metricsServerStream) SendMsg(m interface{}) error {
	err := s.ServerStream.SendMsg(m)

	if s.metrics.opts.EnableMethodLevelMetrics && s.metrics.opts.EnableCallbackMetrics {
		s.metrics.RecordCallback(s.fullMethod, streamType(&grpc.StreamServerInfo{}), "send")
	}

	s.sendCount++

	return err
}

// RecvMsg 接收消息并记录大小
func (s *metricsServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)

	if err == nil && s.metrics.opts.EnableMethodLevelMetrics && s.metrics.opts.EnableCallbackMetrics {
		s.metrics.RecordCallback(s.fullMethod, streamType(&grpc.StreamServerInfo{}), "recv")
	}

	s.recvCount++

	return err
}
