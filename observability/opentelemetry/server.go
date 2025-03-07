package opentelemetry

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dormoron/eidola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ServerInterceptorBuilder is a struct used to configure and build UnaryServerInterceptor
// for gRPC, focusing on metrics like response time, error count, and active request count.
type ServerInterceptorBuilder struct {
	Namespace string        // Namespace for Prometheus metrics
	Subsystem string        // Subsystem is the subset of the namespace
	Name      string        // Name of the metric
	Help      string        // Help provides some description about the metric
	Port      string        // Port where the server is running. If not empty, it will be appended to the address label.
	Timeout   time.Duration // 请求超时时间
	Tracer    trace.Tracer  // OpenTelemetry追踪器
}

// NewServerInterceptorBuilder 创建一个新的服务器拦截器构建器
func NewServerInterceptorBuilder(namespace, subsystem, name, help string) *ServerInterceptorBuilder {
	return &ServerInterceptorBuilder{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Timeout:   30 * time.Second, // 默认30秒超时
		Tracer:    otel.GetTracerProvider().Tracer(fmt.Sprintf("%s.%s.%s", namespace, subsystem, name)),
	}
}

// WithPort 设置端口
func (b *ServerInterceptorBuilder) WithPort(port string) *ServerInterceptorBuilder {
	b.Port = port
	return b
}

// WithTimeout 设置超时时间
func (b *ServerInterceptorBuilder) WithTimeout(timeout time.Duration) *ServerInterceptorBuilder {
	b.Timeout = timeout
	return b
}

// WithTracer 设置自定义追踪器
func (b *ServerInterceptorBuilder) WithTracer(tracer trace.Tracer) *ServerInterceptorBuilder {
	b.Tracer = tracer
	return b
}

// BuildUnary constructs a UnaryServerInterceptor with Prometheus monitoring.
func (b ServerInterceptorBuilder) BuildUnary() grpc.UnaryServerInterceptor {
	address := observability.GetOutboundIP()
	if b.Port != "" {
		address = fmt.Sprintf("%s:%s", address, b.Port) // Use fmt.Sprintf for string concatenation
	}

	// Define Prometheus SummaryVec for response latency
	summaryVec := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_response", b.Name),
		Help:      b.Help,
		// Labels added to all metrics
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
		// Customize objectives
		Objectives: map[float64]float64{
			0.5:   0.01,
			0.75:  0.01,
			0.9:   0.01,
			0.99:  0.001,
			0.999: 0.0001,
		},
	}, []string{"method", "status_code"})

	// Define Prometheus CounterVec for error count
	errCntVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_error_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method", "status_code"})

	// Define Prometheus GaugeVec for active request count
	reqCntVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_active_req_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method"})

	// 添加请求超时计数器
	timeoutCntVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_timeout_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method"})

	// Register defined metrics with Prometheus
	if err := prometheus.Register(summaryVec); err != nil {
		log.Printf("Error registering summaryVec: %v", err)
	}
	if err := prometheus.Register(errCntVec); err != nil {
		log.Printf("Error registering errCntVec: %v", err)
	}
	if err := prometheus.Register(reqCntVec); err != nil {
		log.Printf("Error registering reqCntVec: %v", err)
	}
	if err := prometheus.Register(timeoutCntVec); err != nil {
		log.Printf("Error registering timeoutCntVec: %v", err)
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		// 解析传入的追踪上下文
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		// 设置超时
		if b.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, b.Timeout)
			defer cancel()
		}

		// 从请求元数据中提取跟踪上下文
		propagator := otel.GetTextMapPropagator()
		ctx = propagator.Extract(ctx, metadataCarrier(md))

		// 创建新的span
		methodName := info.FullMethod
		methodParts := strings.Split(methodName, "/")
		spanName := methodName
		if len(methodParts) > 0 {
			spanName = methodParts[len(methodParts)-1]
		}

		ctx, span := b.Tracer.Start(
			ctx,
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.service", methodParts[0]),
				attribute.String("rpc.method", spanName),
			),
		)
		defer span.End()

		// 添加请求统计
		reqCnt := reqCntVec.WithLabelValues(info.FullMethod)
		reqCnt.Inc()
		startTime := time.Now()

		// 执行实际的处理器
		resp, err = handler(ctx, req)

		// 处理结果
		statusCode := grpcCodes.OK
		if err != nil {
			// 从错误中提取gRPC状态码
			st, ok := status.FromError(err)
			if ok {
				statusCode = st.Code()
				// 设置span的状态
				if statusCode != grpcCodes.OK {
					span.SetStatus(codes.Error, st.Message())
					span.RecordError(err, trace.WithAttributes(
						attribute.Int("grpc.status_code", int(statusCode)),
					))
				}
			} else {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			}

			// 记录错误
			errCntVec.WithLabelValues(info.FullMethod, statusCode.String()).Inc()

			// 检查是否是超时错误
			if statusCode == grpcCodes.DeadlineExceeded {
				timeoutCntVec.WithLabelValues(info.FullMethod).Inc()
			}
		} else {
			span.SetStatus(codes.Ok, "")
		}

		// 统计结束
		duration := time.Since(startTime)
		reqCnt.Dec()
		summaryVec.WithLabelValues(info.FullMethod, statusCode.String()).Observe(float64(duration.Milliseconds()))

		return resp, err
	}
}

// BuildStream 构建流式RPC拦截器
func (b ServerInterceptorBuilder) BuildStream() grpc.StreamServerInterceptor {
	address := observability.GetOutboundIP()
	if b.Port != "" {
		address = fmt.Sprintf("%s:%s", address, b.Port)
	}

	// 定义相关指标
	summaryVec := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_stream_response", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
		Objectives: map[float64]float64{
			0.5:  0.01,
			0.9:  0.01,
			0.99: 0.001,
		},
	}, []string{"method", "status_code"})

	errCntVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_stream_error_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method", "status_code"})

	activeCntVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_active_stream_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method"})

	// 注册指标
	prometheus.MustRegister(summaryVec, errCntVec, activeCntVec)

	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 解析传入的追踪上下文
		ctx := ss.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		// 设置超时
		if b.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, b.Timeout)
			defer cancel()
		}

		// 从请求元数据中提取跟踪上下文
		propagator := otel.GetTextMapPropagator()
		ctx = propagator.Extract(ctx, metadataCarrier(md))

		// 创建新的span
		methodName := info.FullMethod
		methodParts := strings.Split(methodName, "/")
		spanName := methodName
		if len(methodParts) > 0 {
			spanName = methodParts[len(methodParts)-1]
		}

		ctx, span := b.Tracer.Start(
			ctx,
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.service", methodParts[0]),
				attribute.String("rpc.method", spanName),
				attribute.Bool("rpc.stream", true),
			),
		)
		defer span.End()

		// 包装流以传播更新的上下文
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          ctx,
		}

		// 统计请求
		activeCntVec.WithLabelValues(info.FullMethod).Inc()
		startTime := time.Now()
		defer func() {
			activeCntVec.WithLabelValues(info.FullMethod).Dec()
		}()

		// 执行处理
		err := handler(srv, wrappedStream)

		// 统计结果
		duration := time.Since(startTime)
		statusCode := grpcCodes.OK
		if err != nil {
			st, ok := status.FromError(err)
			if ok {
				statusCode = st.Code()
				// 设置span的状态
				if statusCode != grpcCodes.OK {
					span.SetStatus(codes.Error, st.Message())
					span.RecordError(err, trace.WithAttributes(
						attribute.Int("grpc.status_code", int(statusCode)),
					))
				}
			} else {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			}

			errCntVec.WithLabelValues(info.FullMethod, statusCode.String()).Inc()
		} else {
			span.SetStatus(codes.Ok, "")
		}

		summaryVec.WithLabelValues(info.FullMethod, statusCode.String()).Observe(float64(duration.Milliseconds()))

		return err
	}
}

// metadataCarrier 实现了TextMapCarrier接口用于从gRPC元数据中提取和注入追踪上下文
type metadataCarrier metadata.MD

func (m metadataCarrier) Get(key string) string {
	values := metadata.MD(m).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (m metadataCarrier) Set(key string, value string) {
	metadata.MD(m).Set(key, value)
}

func (m metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range metadata.MD(m) {
		keys = append(keys, k)
	}
	return keys
}

// wrappedServerStream 包装gRPC ServerStream，将上下文传递给流处理器
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}
