package ratelimit

import (
	"context"
	"errors"
	"google.golang.org/grpc"
)

type rejectStrategy func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error)

var defaultRejectStrategy rejectStrategy = func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return nil, errors.New("Rate limit reached, please try again later.")
}

var markFailedStrategy rejectStrategy = func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx = context.WithValue(ctx, "limited", true)
	return handler(ctx, req)
}
