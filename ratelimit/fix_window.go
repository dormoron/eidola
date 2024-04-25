package ratelimit

import (
	"context"
	"google.golang.org/grpc"
	"sync/atomic"
	"time"
)

type FixWindowLimiterOptions func(l *FixWindowLimiter)

type FixWindowLimiter struct {
	timestamp int64

	interval int64

	rate int64
	cnt  int64

	onReject rejectStrategy
}

func NewFixWindowLimiter(interval time.Duration, rate int64) *FixWindowLimiter {
	return &FixWindowLimiter{
		interval:  interval.Nanoseconds(),
		timestamp: time.Now().UnixNano(),
		rate:      rate,
		onReject:  defaultRejectStrategy,
	}
}

func FixWindowMarkFailed() FixWindowLimiterOptions {
	return func(l *FixWindowLimiter) {
		l.onReject = markFailedStrategy
	}
}

func (l *FixWindowLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		current := time.Now().UnixNano()
		timestamp := atomic.LoadInt64(&l.timestamp)
		cnt := atomic.LoadInt64(&l.cnt)
		if timestamp+l.interval < current {
			if atomic.CompareAndSwapInt64(&l.timestamp, timestamp, current) {
				atomic.CompareAndSwapInt64(&l.cnt, cnt, 0)
			}
		}
		cnt = atomic.AddInt64(&l.cnt, 1)
		if cnt > l.rate {
			return l.onReject(ctx, req, info, handler)
		}
		resp, err = handler(ctx, req)
		return
	}
}
