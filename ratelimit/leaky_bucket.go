package ratelimit

import (
	"context"
	"google.golang.org/grpc"
	"time"
)

// LeakyBucketLimiterOptions defines a function type for setting options on LeakyBucketLimiter.
type LeakyBucketLimiterOptions func(l *LeakyBucketLimiter)

// LeakyBucketLimiter implements rate limiting using the leaky bucket algorithm.
type LeakyBucketLimiter struct {
	producer *time.Ticker   // Producer issues tokens at a fixed interval.
	onReject rejectStrategy // onReject defines the strategy to handle rejected requests.
}

// NewLeakyBucketLimiter initializes and returns a new LeakyBucketLimiter.
func NewLeakyBucketLimiter(interval time.Duration) *LeakyBucketLimiter {
	return &LeakyBucketLimiter{
		producer: time.NewTicker(interval), // Initialize the ticker with the given interval.
		onReject: defaultRejectStrategy,    // Set the default rejection strategy.
	}
}

// LeakyBucketMarkFailed provides an option to set the markFailedStrategy as the rejection strategy.
func LeakyBucketMarkFailed() LeakyBucketLimiterOptions {
	return func(l *LeakyBucketLimiter) {
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that imposes rate limiting based on the leaky bucket algorithm.
func (l *LeakyBucketLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		select {
		case <-ctx.Done(): // Context cancellation or deadline exceeded.
			// Use the rejection strategy when the request is preempted or the context is cancelled.
			return l.onReject(ctx, req, info, handler)
		case <-l.producer.C:
			// Process the incoming request when a token is available.
			return handler(ctx, req)
		}
	}
}

// Close stops the ticker and releases associated resources.
func (l *LeakyBucketLimiter) Close() error {
	l.producer.Stop()
	return nil
}
