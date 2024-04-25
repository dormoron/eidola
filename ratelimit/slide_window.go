package ratelimit

import (
	"container/list"
	"context"
	"google.golang.org/grpc"
	"sync"
	"time"
)

type SlideWindowLimiterOptions func(l *SlideWindowLimiter)

type SlideWindowLimiter struct {
	queue    *list.List
	interval int64
	rate     int
	mutex    sync.Mutex
	onReject rejectStrategy
}

func NewSlideWindowLimiter(interval time.Duration, rate int) *SlideWindowLimiter {
	return &SlideWindowLimiter{
		queue:    list.New(),
		interval: interval.Nanoseconds(),
		rate:     rate,
		onReject: defaultRejectStrategy,
	}
}

func SlideWindowMarkFailed() SlideWindowLimiterOptions {
	return func(l *SlideWindowLimiter) {
		l.onReject = markFailedStrategy
	}
}

func (l *SlideWindowLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		now := time.Now().UnixNano()
		boundary := now - l.interval
		l.mutex.Lock()
		length := l.queue.Len()
		if length < l.rate {
			resp, err = handler(ctx, req)
			l.queue.PushBack(now)
			l.mutex.Unlock()
			return
		}
		timestamp := l.queue.Front()
		for timestamp != nil && timestamp.Value.(int64) < boundary {
			l.queue.Remove(timestamp)
			timestamp = l.queue.Front()
		}
		length = l.queue.Len()
		l.mutex.Unlock()
		if length >= l.rate {
			return l.onReject(ctx, req, info, handler)
		}
		resp, err = handler(ctx, req)
		l.queue.PushBack(now)
		return
	}
}
