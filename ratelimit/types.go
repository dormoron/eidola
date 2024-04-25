package ratelimit

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Limiter interface {
	LimitUnary() grpc.UnaryServerInterceptor
}

type rejectStrategy func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error)

var defaultRejectStrategy rejectStrategy = func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return nil, status.Errorf(codes.ResourceExhausted, "Rate limit reached, please try again later %s", info.FullMethod)
}

var markFailedStrategy rejectStrategy = func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx = context.WithValue(ctx, "limited", true)
	return handler(ctx, req)
}
