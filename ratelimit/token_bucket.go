package ratelimit

import (
	"context"
	"google.golang.org/grpc"
	"time"
)

type TokenBucketLimiterOptions func(l *TokenBucketLimiter)

type TokenBucketLimiter struct {
	tokens   chan struct{}
	close    chan struct{}
	onReject rejectStrategy
}

func NewTokenBucketLimiter(capacity int, interval time.Duration) *TokenBucketLimiter {
	ch := make(chan struct{}, capacity)
	closeCh := make(chan struct{})
	producer := time.NewTicker(interval)
	go func() {
		defer producer.Stop()
		for {
			select {
			case <-producer.C:
				select {
				case ch <- struct{}{}:
				default:
				}
			case <-closeCh:
				return
			}
		}
	}()
	return &TokenBucketLimiter{
		tokens:   ch,
		close:    closeCh,
		onReject: defaultRejectStrategy,
	}
}

func TokenBucketMarkFailed() TokenBucketLimiterOptions {
	return func(l *TokenBucketLimiter) {
		l.onReject = markFailedStrategy
	}
}

func (l *TokenBucketLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		select {
		case <-l.close:
			resp, err = handler(ctx, req)
		case <-l.tokens:
			resp, err = handler(ctx, req)
		case <-ctx.Done():
			return l.onReject(ctx, req, info, handler)
		}
		return
	}
}

func (l *TokenBucketLimiter) Close() error {
	close(l.close)
	return nil
}
