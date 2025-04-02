package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dormoron/eidola/plugin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// PrometheusPlugin 是一个用于收集Prometheus指标的插件
type PrometheusPlugin struct {
	// 配置
	config Config
	// HTTP服务器
	server *http.Server
	// 指标注册器
	registry *prometheus.Registry
	// 收集的指标
	metrics *Metrics
}

// Config 是Prometheus插件的配置
type Config struct {
	// 指标的命名空间
	Namespace string
	// HTTP服务器的地址
	HTTPAddr string
	// 指标路径
	MetricsPath string
	// 是否启用
	Enabled bool
	// 收集的指标类型
	EnabledMetrics MetricTypes
}

// MetricTypes 表示启用的指标类型
type MetricTypes struct {
	// 请求计数器
	RequestCounter bool
	// 请求延迟
	RequestLatency bool
	// 请求大小
	RequestSize bool
	// 响应大小
	ResponseSize bool
	// 活跃连接数
	ActiveConnections bool
	// 活跃流数
	ActiveStreams bool
}

// Metrics 包含所有注册的指标
type Metrics struct {
	// 请求计数
	RequestCounter *prometheus.CounterVec
	// 请求延迟
	RequestLatency *prometheus.HistogramVec
	// 请求大小
	RequestSize *prometheus.HistogramVec
	// 响应大小
	ResponseSize *prometheus.HistogramVec
	// 活跃连接数
	ActiveConnections prometheus.Gauge
	// 活跃流数
	ActiveStreams *prometheus.GaugeVec
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Namespace:   "eidola",
		HTTPAddr:    ":9090",
		MetricsPath: "/metrics",
		Enabled:     true,
		EnabledMetrics: MetricTypes{
			RequestCounter:    true,
			RequestLatency:    true,
			RequestSize:       true,
			ResponseSize:      true,
			ActiveConnections: true,
			ActiveStreams:     true,
		},
	}
}

// NewPrometheusPlugin 创建一个新的Prometheus插件
func NewPrometheusPlugin(config Config) *PrometheusPlugin {
	return &PrometheusPlugin{
		config:   config,
		registry: prometheus.NewRegistry(),
	}
}

// Name 实现Plugin接口
func (p *PrometheusPlugin) Name() string {
	return "prometheus_metrics"
}

// Init 实现Plugin接口
func (p *PrometheusPlugin) Init() error {
	if !p.config.Enabled {
		return nil
	}

	// 初始化指标
	p.metrics = &Metrics{}

	// 创建并注册所有配置的指标
	if p.config.EnabledMetrics.RequestCounter {
		p.metrics.RequestCounter = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: p.config.Namespace,
				Name:      "grpc_requests_total",
				Help:      "Total number of gRPC requests completed",
			},
			[]string{"service", "method", "code"},
		)
		p.registry.MustRegister(p.metrics.RequestCounter)
	}

	if p.config.EnabledMetrics.RequestLatency {
		p.metrics.RequestLatency = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: p.config.Namespace,
				Name:      "grpc_request_duration_seconds",
				Help:      "Histogram of gRPC request latency in seconds",
				Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"service", "method"},
		)
		p.registry.MustRegister(p.metrics.RequestLatency)
	}

	if p.config.EnabledMetrics.RequestSize {
		p.metrics.RequestSize = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: p.config.Namespace,
				Name:      "grpc_request_size_bytes",
				Help:      "Histogram of gRPC request size in bytes",
				Buckets:   prometheus.ExponentialBuckets(1, 2, 20),
			},
			[]string{"service", "method"},
		)
		p.registry.MustRegister(p.metrics.RequestSize)
	}

	if p.config.EnabledMetrics.ResponseSize {
		p.metrics.ResponseSize = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: p.config.Namespace,
				Name:      "grpc_response_size_bytes",
				Help:      "Histogram of gRPC response size in bytes",
				Buckets:   prometheus.ExponentialBuckets(1, 2, 20),
			},
			[]string{"service", "method"},
		)
		p.registry.MustRegister(p.metrics.ResponseSize)
	}

	if p.config.EnabledMetrics.ActiveConnections {
		p.metrics.ActiveConnections = prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: p.config.Namespace,
				Name:      "grpc_active_connections",
				Help:      "Current number of active gRPC connections",
			},
		)
		p.registry.MustRegister(p.metrics.ActiveConnections)
	}

	if p.config.EnabledMetrics.ActiveStreams {
		p.metrics.ActiveStreams = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: p.config.Namespace,
				Name:      "grpc_active_streams",
				Help:      "Current number of active gRPC streams",
			},
			[]string{"service", "method"},
		)
		p.registry.MustRegister(p.metrics.ActiveStreams)
	}

	// 启动HTTP服务器提供指标
	return p.startHTTPServer()
}

// startHTTPServer 启动HTTP服务器来提供Prometheus指标
func (p *PrometheusPlugin) startHTTPServer() error {
	mux := http.NewServeMux()
	mux.Handle(p.config.MetricsPath, promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{}))

	p.server = &http.Server{
		Addr:    p.config.HTTPAddr,
		Handler: mux,
	}

	go func() {
		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Prometheus HTTP server error: %v\n", err)
		}
	}()

	return nil
}

// Close 实现Plugin接口
func (p *PrometheusPlugin) Close() error {
	if p.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return p.server.Shutdown(ctx)
	}
	return nil
}

// RegisterWithServer 实现ServerPlugin接口
func (p *PrometheusPlugin) RegisterWithServer(*grpc.Server) error {
	// 不需要直接向服务器注册服务，而是通过拦截器收集指标
	return nil
}

// UnaryServerInterceptor 实现InterceptorPlugin接口
func (p *PrometheusPlugin) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !p.config.Enabled {
			return handler(ctx, req)
		}

		serviceName, methodName := splitMethodName(info.FullMethod)

		// 开始时间
		startTime := time.Now()

		// 跟踪活跃连接
		if p.metrics.ActiveConnections != nil {
			p.metrics.ActiveConnections.Inc()
			defer p.metrics.ActiveConnections.Dec()
		}

		// 跟踪请求大小
		if p.metrics.RequestSize != nil {
			// 这里简单地使用一个估计值作为请求大小
			// 在实际应用中，您可能需要实现一个更准确的方法来计算大小
			requestSize := estimateSize(req)
			p.metrics.RequestSize.WithLabelValues(serviceName, methodName).Observe(float64(requestSize))
		}

		// 处理请求
		resp, err := handler(ctx, req)

		// 计算延迟
		latency := time.Since(startTime).Seconds()

		// 记录延迟
		if p.metrics.RequestLatency != nil {
			p.metrics.RequestLatency.WithLabelValues(serviceName, methodName).Observe(latency)
		}

		// 获取状态码
		code := status.Code(err)
		if p.metrics.RequestCounter != nil {
			p.metrics.RequestCounter.WithLabelValues(serviceName, methodName, code.String()).Inc()
		}

		// 跟踪响应大小
		if p.metrics.ResponseSize != nil && resp != nil {
			// 同样，这是一个估计值
			responseSize := estimateSize(resp)
			p.metrics.ResponseSize.WithLabelValues(serviceName, methodName).Observe(float64(responseSize))
		}

		return resp, err
	}
}

// StreamServerInterceptor 实现InterceptorPlugin接口
func (p *PrometheusPlugin) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !p.config.Enabled {
			return handler(srv, ss)
		}

		serviceName, methodName := splitMethodName(info.FullMethod)

		// 开始时间
		startTime := time.Now()

		// 跟踪活跃连接和流
		if p.metrics.ActiveConnections != nil {
			p.metrics.ActiveConnections.Inc()
			defer p.metrics.ActiveConnections.Dec()
		}

		if p.metrics.ActiveStreams != nil {
			p.metrics.ActiveStreams.WithLabelValues(serviceName, methodName).Inc()
			defer p.metrics.ActiveStreams.WithLabelValues(serviceName, methodName).Dec()
		}

		// 包装流以跟踪请求和响应大小
		wrappedStream := &monitoredServerStream{
			ServerStream: ss,
			plugin:       p,
			serviceName:  serviceName,
			methodName:   methodName,
		}

		// 处理流
		err := handler(srv, wrappedStream)

		// 计算延迟
		latency := time.Since(startTime).Seconds()

		// 记录延迟
		if p.metrics.RequestLatency != nil {
			p.metrics.RequestLatency.WithLabelValues(serviceName, methodName).Observe(latency)
		}

		// 获取状态码
		code := status.Code(err)
		if p.metrics.RequestCounter != nil {
			p.metrics.RequestCounter.WithLabelValues(serviceName, methodName, code.String()).Inc()
		}

		return err
	}
}

// UnaryClientInterceptor 实现InterceptorPlugin接口
func (p *PrometheusPlugin) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return nil // 这个插件只关注服务端指标
}

// StreamClientInterceptor 实现InterceptorPlugin接口
func (p *PrometheusPlugin) StreamClientInterceptor() grpc.StreamClientInterceptor {
	return nil // 这个插件只关注服务端指标
}

// monitoredServerStream 包装了 grpc.ServerStream，用于跟踪流请求和响应大小
type monitoredServerStream struct {
	grpc.ServerStream
	plugin      *PrometheusPlugin
	serviceName string
	methodName  string
}

func (s *monitoredServerStream) SendMsg(m interface{}) error {
	err := s.ServerStream.SendMsg(m)
	if err == nil && s.plugin.metrics.ResponseSize != nil {
		responseSize := estimateSize(m)
		s.plugin.metrics.ResponseSize.WithLabelValues(s.serviceName, s.methodName).Observe(float64(responseSize))
	}
	return err
}

func (s *monitoredServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)
	if err == nil && s.plugin.metrics.RequestSize != nil {
		requestSize := estimateSize(m)
		s.plugin.metrics.RequestSize.WithLabelValues(s.serviceName, s.methodName).Observe(float64(requestSize))
	}
	return err
}

// splitMethodName 从完整的方法名中提取服务名和方法名
func splitMethodName(fullMethod string) (string, string) {
	// 格式通常是 "/package.service/method"
	if len(fullMethod) == 0 || fullMethod[0] != '/' {
		return "unknown", "unknown"
	}

	// 跳过前导斜杠
	fullMethod = fullMethod[1:]
	pos := 0

	// 找到第一个斜杠，它分隔服务名和方法名
	for i := 0; i < len(fullMethod); i++ {
		if fullMethod[i] == '/' {
			pos = i
			break
		}
	}

	if pos == 0 {
		return fullMethod, "unknown"
	}

	return fullMethod[:pos], fullMethod[pos+1:]
}

// estimateSize 估计一个对象的大小（字节数）
// 这是一个简单的估计方法，实际应用中可能需要更准确的测量
func estimateSize(obj interface{}) int64 {
	if obj == nil {
		return 0
	}

	// 这里实现一个简单的估计算法
	// 实际上这只是一个占位实现，真实生产环境需要更准确的方法
	return 1024 // 返回一个固定大小作为示例
}

// 确保PrometheusPlugin实现了所有需要的接口
var _ plugin.Plugin = (*PrometheusPlugin)(nil)
var _ plugin.ServerPlugin = (*PrometheusPlugin)(nil)
var _ plugin.InterceptorPlugin = (*PrometheusPlugin)(nil)
