package ratelimit

import (
	"context"
	"github.com/dormoron/eidola/internal/errs"
	"google.golang.org/grpc"
	"sync"
	"time"
)

// TokenBucketLimiterOptions defines the type for configuration options on the TokenBucketLimiter.
type TokenBucketLimiterOptions func(l *TokenBucketLimiter)

// TokenBucketLimiter controls access to a resource by enforcing a rate limit using a token bucket strategy.
type TokenBucketLimiter struct {
	tokens   chan struct{}  // Channel used to represent tokens that control rate limiting.
	close    chan struct{}  // Channel used to signal shutdown.
	onReject rejectStrategy // Strategy to be used when a request is rejected.
	mutex    sync.Mutex     // Mutex to protect shared state.
}

// NewTokenBucketLimiter constructs a new TokenBucketLimiter with the given capacity and token refill interval.
func NewTokenBucketLimiter(capacity int, interval time.Duration) *TokenBucketLimiter {
	tokensCh := make(chan struct{}, capacity)
	closeCh := make(chan struct{})

	producer := time.NewTicker(interval)
	go func() {
		defer producer.Stop()
		for {
			select {
			case <-producer.C: // Refill token.
				select {
				case tokensCh <- struct{}{}: // Attempt to add a token.
				default: // Skip if channel is already full.
				}
			case <-closeCh: // Stop the goroutine if close signal is received.
				return
			}
		}
	}()

	return &TokenBucketLimiter{
		tokens:   tokensCh,
		close:    closeCh,
		onReject: defaultRejectStrategy, // Set default reject strategy on new limiter.
	}
}

// TokenBucketMarkFailed provides a configuration option to use the markFailedStrategy when a request is rejected.
func TokenBucketMarkFailed() TokenBucketLimiterOptions {
	return func(l *TokenBucketLimiter) {
		l.mutex.Lock()
		defer l.mutex.Unlock()
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that enforces the token bucket rate limiting.
func (l *TokenBucketLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		select {
		case <-l.tokens: // Allow the request to be handled if a token is available.
			return handler(ctx, req)
		case <-ctx.Done(): // Reject the request if the provided context is already done.
			return l.onReject(ctx, req, info, handler)
		case <-l.close: // Reject the request if the limiter is being closed.
			return nil, errs.ErrRateLimitClose()
		}
	}
}

// Close cleanly stops the token refill and shuts down the limiter.
func (l *TokenBucketLimiter) Close() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.close != nil {
		close(l.close)
		l.close = nil // Prevent closing the channel more than once and causing a panic.
	}

	return nil
}
