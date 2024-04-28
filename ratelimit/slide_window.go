package ratelimit

import (
	"container/list"
	"context"
	"google.golang.org/grpc"
	"sync"
	"time"
)

// SlideWindowLimiterOptions defines the function signature for configuration options of SlideWindowLimiter.
type SlideWindowLimiterOptions func(l *SlideWindowLimiter)

// SlideWindowLimiter implements rate limiting using a sliding window approach.
type SlideWindowLimiter struct {
	queue    *list.List     // Queue holds the timestamps of incoming requests.
	interval int64          // Interval defines the sliding window width in nanoseconds.
	rate     int            // Rate defines the maximum number of requests allowed within the interval.
	mutex    sync.Mutex     // Mutex ensures atomic access to the queue.
	onReject rejectStrategy // OnReject is a strategy executed when a request is rejected.
}

// NewSlideWindowLimiter returns a new instance of SlideWindowLimiter with the specified interval and rate.
func NewSlideWindowLimiter(interval time.Duration, rate int) *SlideWindowLimiter {
	return &SlideWindowLimiter{
		queue:    list.New(),
		interval: interval.Nanoseconds(),
		rate:     rate,
		onReject: defaultRejectStrategy, // Set the default reject strategy on new SlideWindowLimiter.
	}
}

// SlideWindowMarkFailed provides an option to set the markFailedStrategy as the reject strategy.
func SlideWindowMarkFailed() SlideWindowLimiterOptions {
	return func(l *SlideWindowLimiter) {
		l.mutex.Lock()         // Protect access to onReject to ensure thread safety.
		defer l.mutex.Unlock() // Defer unlock so it executes regardless of where the method exits.
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that imposes rate limiting based on the sliding window mechanism.
func (l *SlideWindowLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		now := time.Now().UnixNano()
		boundary := now - l.interval

		l.mutex.Lock()
		defer l.mutex.Unlock() // Use defer to ensure the mutex is always unlocked after locking.

		// Remove older timestamps from the queue which are outside of the sliding window.
		for element := l.queue.Front(); element != nil; {
			next := element.Next() // Save next element because current might get removed.
			if element.Value.(int64) < boundary {
				l.queue.Remove(element)
			}
			element = next // Move to next saved element.
		}

		if l.queue.Len() < l.rate {
			// There is room in the sliding window for this request.
			l.queue.PushBack(now)
			resp, err = handler(ctx, req)
			return
		}

		// Exceeded the rate limit, execute the reject strategy.
		return l.onReject(ctx, req, info, handler)
	}
}
