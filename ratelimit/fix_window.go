package ratelimit

import (
	"context"
	"google.golang.org/grpc"
	"sync/atomic"
	"time"
)

// FixWindowLimiterOptions defines a function type for setting options on FixWindowLimiter.
type FixWindowLimiterOptions func(l *FixWindowLimiter)

// FixWindowLimiter implements rate limiting using a fixed time window algorithm.
type FixWindowLimiter struct {
	timestamp int64 // Timestamp of the start of the current window.
	interval  int64 // Interval is the duration of the window in nanoseconds.
	rate      int64 // Rate is the number of requests allowed per window.
	cnt       int64 // Cnt is the current count of requests in the window.

	onReject rejectStrategy // onReject is called when a request exceeds the rate limit.
}

// NewFixWindowLimiter creates a new FixWindowLimiter with the given interval and rate.
func NewFixWindowLimiter(interval time.Duration, rate int64) *FixWindowLimiter {
	return &FixWindowLimiter{
		interval:  interval.Nanoseconds(),
		timestamp: time.Now().UnixNano(),
		rate:      rate,
		onReject:  defaultRejectStrategy, // Set the default rejection strategy.
	}
}

// FixWindowMarkFailed provides an option to set the markFailedStrategy as the rejection strategy.
func FixWindowMarkFailed() FixWindowLimiterOptions {
	return func(l *FixWindowLimiter) {
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that imposes rate limiting based on the fixed window algorithm.
func (l *FixWindowLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		current := time.Now().UnixNano()

		// Load the current state atomically.
		timestamp := atomic.LoadInt64(&l.timestamp)
		cnt := atomic.LoadInt64(&l.cnt)

		// Check if the current window is expired and reset the counter if true.
		if current-timestamp >= l.interval {
			if atomic.CompareAndSwapInt64(&l.timestamp, timestamp, current) {
				atomic.StoreInt64(&l.cnt, 0)
			}
		}

		// Increment the request count and check the rate.
		cnt = atomic.AddInt64(&l.cnt, 1)
		if cnt > l.rate {
			// Reject the request as the rate has been exceeded.
			return l.onReject(ctx, req, info, handler)
		}

		// Process the request as the rate limit has not been reached.
		return handler(ctx, req)
	}
}
