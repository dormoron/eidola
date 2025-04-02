package opentelemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Config 定义OpenTelemetry的配置选项
type Config struct {
	// 服务名称
	ServiceName string
	// 服务版本
	ServiceVersion string
	// 环境名称
	Environment string
	// OTLP Endpoint
	Endpoint string
	// 采样率 (0.0-1.0)
	SamplingRatio float64
	// 是否启用日志
	EnableLogs bool
	// 是否启用跟踪
	EnableTraces bool
	// 是否启用指标
	EnableMetrics bool
	// 导出器超时
	ExporterTimeout time.Duration
	// 附加资源属性
	ResourceAttributes map[string]string
	// gRPC连接选项
	GRPCOptions []grpc.DialOption
}

// DefaultConfig 返回OpenTelemetry的默认配置
func DefaultConfig() Config {
	return Config{
		ServiceName:     "unknown-service",
		ServiceVersion:  "0.1.0",
		Environment:     "development",
		Endpoint:        "localhost:4317",
		SamplingRatio:   1.0,
		EnableTraces:    true,
		EnableMetrics:   true,
		EnableLogs:      true,
		ExporterTimeout: 10 * time.Second,
	}
}

// Provider 封装了OpenTelemetry的初始化和关闭逻辑
type Provider struct {
	config         Config
	tracerProvider *sdktrace.TracerProvider
	shutdown       func(context.Context) error
}

// NewProvider 创建并初始化一个新的OpenTelemetry Provider
func NewProvider(config Config) (*Provider, error) {
	if config.ServiceName == "" {
		return nil, fmt.Errorf("service name is required")
	}

	// 创建资源
	res, err := createResource(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// 创建OTLP导出器
	exporter, err := createExporter(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create exporter: %w", err)
	}

	// 创建采样器
	sampler := sdktrace.ParentBased(
		sdktrace.TraceIDRatioBased(config.SamplingRatio),
	)

	// 创建并配置Tracer Provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	// 设置全局Tracer Provider
	otel.SetTracerProvider(tp)

	// 设置全局传播器
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// 创建关闭函数
	shutdown := func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}

	return &Provider{
		config:         config,
		tracerProvider: tp,
		shutdown:       shutdown,
	}, nil
}

// createResource 创建OpenTelemetry资源配置
func createResource(config Config) (*resource.Resource, error) {
	// 基础资源属性
	attrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(config.ServiceName),
		semconv.ServiceVersionKey.String(config.ServiceVersion),
		attribute.String("environment", config.Environment),
		attribute.String("library.name", "eidola"),
	}

	// 添加自定义资源属性
	if len(config.ResourceAttributes) > 0 {
		for k, v := range config.ResourceAttributes {
			attrs = append(attrs, attribute.String(k, v))
		}
	}

	return resource.NewWithAttributes(
		semconv.SchemaURL,
		attrs...,
	), nil
}

// createExporter 创建OTLP跟踪导出器
func createExporter(config Config) (sdktrace.SpanExporter, error) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, config.ExporterTimeout)
	defer cancel()

	options := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.Endpoint),
		otlptracegrpc.WithTimeout(config.ExporterTimeout),
	}

	// 添加自定义的gRPC选项
	if len(config.GRPCOptions) > 0 {
		options = append(options, otlptracegrpc.WithDialOption(config.GRPCOptions...))
	}

	// 默认情况下使用不安全连接
	if len(config.GRPCOptions) == 0 {
		options = append(options, otlptracegrpc.WithInsecure())
	}

	client := otlptracegrpc.NewClient(options...)
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	return exporter, nil
}

// Shutdown 关闭OpenTelemetry Provider
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.shutdown != nil {
		return p.shutdown(ctx)
	}
	return nil
}

// TracerProvider 返回OpenTelemetry的TracerProvider
func (p *Provider) TracerProvider() trace.TracerProvider {
	return p.tracerProvider
}

// Tracer 返回OpenTelemetry的Tracer
func (p *Provider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return p.tracerProvider.Tracer(name, opts...)
}

// GRPCUnaryClientInterceptor 创建一个gRPC客户端一元拦截器，用于跟踪请求
func GRPCUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		// 获取当前服务的tracer
		tr := otel.Tracer("eidola.client")

		// 创建新的span
		ctx, span := tr.Start(ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.method", method),
				attribute.String("peer.service", cc.Target()),
			),
		)
		defer span.End()

		// 序列化请求参数，并添加为span属性（小心大对象）
		if reqBytes, err := json.Marshal(req); err == nil && len(reqBytes) < 1024 {
			span.SetAttributes(attribute.String("rpc.request", string(reqBytes)))
		}

		// 注入跟踪上下文到gRPC元数据
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		// 将跟踪信息传播到元数据
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(md))
		ctx = metadata.NewOutgoingContext(ctx, md)

		// 调用RPC方法
		err := invoker(ctx, method, req, reply, cc, opts...)

		// 根据RPC调用结果设置span状态
		if err != nil {
			span.RecordError(err)
		}

		// 序列化响应参数，并添加为span属性（小心大对象）
		if replyBytes, err := json.Marshal(reply); err == nil && len(replyBytes) < 1024 {
			span.SetAttributes(attribute.String("rpc.response", string(replyBytes)))
		}

		return err
	}
}

// GRPCStreamClientInterceptor 创建一个gRPC客户端流拦截器，用于跟踪流请求
func GRPCStreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		// 获取当前服务的tracer
		tr := otel.Tracer("eidola.client")

		// 创建新的span
		ctx, span := tr.Start(ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.method", method),
				attribute.String("peer.service", cc.Target()),
				attribute.Bool("rpc.stream", true),
			),
		)

		// 注入跟踪上下文到gRPC元数据
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		// 将跟踪信息传播到元数据
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(md))
		ctx = metadata.NewOutgoingContext(ctx, md)

		// 调用原始的流创建器
		clientStream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			span.RecordError(err)
			span.End()
			return nil, err
		}

		// 返回包装后的流，在流关闭时结束span
		return &tracedClientStream{
			ClientStream: clientStream,
			span:         span,
		}, nil
	}
}

// tracedClientStream 是一个包装了原始ClientStream的结构体，用于在流结束时关闭span
type tracedClientStream struct {
	grpc.ClientStream
	span trace.Span
}

func (s *tracedClientStream) SendMsg(m interface{}) error {
	err := s.ClientStream.SendMsg(m)
	if err != nil {
		s.span.RecordError(err)
	}
	return err
}

func (s *tracedClientStream) RecvMsg(m interface{}) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		s.span.RecordError(err)
	}
	return err
}

func (s *tracedClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	if err != nil {
		s.span.RecordError(err)
	}
	s.span.End()
	return err
}

// GRPCUnaryServerInterceptor 创建一个gRPC服务端一元拦截器，用于跟踪请求
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// 从gRPC元数据中提取跟踪上下文
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		// 从元数据中提取跟踪信息
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(md))

		// 获取当前服务的tracer
		tr := otel.Tracer("eidola.server")

		// 创建新的span
		ctx, span := tr.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.service", info.FullMethod),
			),
		)
		defer span.End()

		// 序列化请求参数，并添加为span属性（小心大对象）
		if reqBytes, err := json.Marshal(req); err == nil && len(reqBytes) < 1024 {
			span.SetAttributes(attribute.String("rpc.request", string(reqBytes)))
		}

		// 处理请求
		resp, err := handler(ctx, req)

		// 处理错误
		if err != nil {
			span.RecordError(err)
		}

		// 序列化响应参数，并添加为span属性（小心大对象）
		if resp != nil {
			if respBytes, err := json.Marshal(resp); err == nil && len(respBytes) < 1024 {
				span.SetAttributes(attribute.String("rpc.response", string(respBytes)))
			}
		}

		return resp, err
	}
}

// GRPCStreamServerInterceptor 创建一个gRPC服务端流拦截器，用于跟踪流请求
func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// 获取上下文
		ctx := ss.Context()

		// 从gRPC元数据中提取跟踪上下文
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		// 从元数据中提取跟踪信息
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(md))

		// 获取当前服务的tracer
		tr := otel.Tracer("eidola.server")

		// 创建新的span
		ctx, span := tr.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.service", info.FullMethod),
				attribute.Bool("rpc.stream", true),
			),
		)
		defer span.End()

		// 包装ServerStream，以传递附带跟踪信息的上下文
		wrappedStream := &tracedServerStream{
			ServerStream: ss,
			ctx:          ctx,
			span:         span,
		}

		// 处理流请求
		err := handler(srv, wrappedStream)
		if err != nil {
			span.RecordError(err)
		}

		return err
	}
}

// tracedServerStream 是一个包装了原始ServerStream的结构体，用于传递带有跟踪信息的上下文
type tracedServerStream struct {
	grpc.ServerStream
	ctx  context.Context
	span trace.Span
}

func (s *tracedServerStream) Context() context.Context {
	return s.ctx
}

func (s *tracedServerStream) SendMsg(m interface{}) error {
	err := s.ServerStream.SendMsg(m)
	if err != nil {
		s.span.RecordError(err)
	}
	return err
}

func (s *tracedServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)
	if err != nil {
		s.span.RecordError(err)
	}
	return err
}
