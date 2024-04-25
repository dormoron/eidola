package opentelemetry

import (
	"context"
	"fmt"
	"github.com/dormoron/eidola/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

type ClientOtelBuilder struct {
	Tracer trace.Tracer
	Port   int
}

func (b *ClientOtelBuilder) Build() grpc.UnaryServerInterceptor {
	if b.Tracer == nil {
		b.Tracer = otel.GetTracerProvider().Tracer(instrumentationName)
	}
	addr := observability.GetOutboundIP()
	if b.Port != 0 {
		addr = fmt.Sprintf("%s:%d", addr, b.Port)
	}
	return func(ctx context.Context, req interface{},
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		spanCtx, span := b.Tracer.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindClient))
		span.SetAttributes(attribute.String("address", addr))
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			}
			span.End()
		}()
		resp, err = handler(spanCtx, req)
		return
	}
}
