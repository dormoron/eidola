package ratelimit

import (
	"context"
	"google.golang.org/grpc"
	"time"
)

type LeakyBucketLimiterOptions func(l *LeakyBucketLimiter)

type LeakyBucketLimiter struct {
	producer *time.Ticker
	onReject rejectStrategy
}

func NewLeakyBucketLimiter(interval time.Duration) *LeakyBucketLimiter {
	return &LeakyBucketLimiter{
		producer: time.NewTicker(interval),
		onReject: defaultRejectStrategy,
	}
}

func LeakyBucketMarkFailed() LeakyBucketLimiterOptions {
	return func(l *LeakyBucketLimiter) {
		l.onReject = markFailedStrategy
	}
}

func (l *LeakyBucketLimiter) BuildServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		select {
		case <-ctx.Done():
			return l.onReject(ctx, req, info, handler)
		case <-l.producer.C:
			resp, err = handler(ctx, req)
		}
		return
	}
}

func (l *LeakyBucketLimiter) Close() error {
	l.producer.Stop()
	return nil
}
