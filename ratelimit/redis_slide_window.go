package ratelimit

import (
	"context"
	_ "embed"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"time"
)

//go:embed lua/fix_window.lua
var luaSlideWindow string

// RedisSlideWindowLimiterOptions defines the function signature for configuration options of RedisSlideWindowLimiter.
type RedisSlideWindowLimiterOptions func(l *RedisSlideWindowLimiter)

// RedisSlideWindowLimiter implements rate limiting using a sliding window approach with redis.
type RedisSlideWindowLimiter struct {
	client   redis.Cmdable  // Client is the redis client for rate limiting.
	service  string         // Service is the identifier for the service to limit.
	interval time.Duration  // Interval defines the sliding window width.
	rate     int            // Rate defines the maximum number of requests allowed within the interval.
	onReject rejectStrategy // OnReject is a strategy executed when a request is rejected.
}

// NewRedisSlideWindowLimiter returns a new instance of RedisSlideWindowLimiter with the provided configuration.
func NewRedisSlideWindowLimiter(client redis.Cmdable, service string, interval time.Duration, rate int) *RedisSlideWindowLimiter {
	return &RedisSlideWindowLimiter{
		client:   client,
		service:  service,
		interval: interval,
		rate:     rate,
		onReject: defaultRejectStrategy, // Set the default reject strategy on new RedisSlideWindowLimiter.
	}
}

// RedisSlideWindowMarkFailed provides an option to set the markFailedStrategy as the reject strategy.
func RedisSlideWindowMarkFailed() RedisSlideWindowLimiterOptions {
	return func(l *RedisSlideWindowLimiter) {
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that imposes rate limiting based on the sliding window mechanism.
func (l *RedisSlideWindowLimiter) LimitUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		overLimit, err := l.limit(ctx)
		if err != nil {
			// Handle error returned by limit()
			return nil, err
		}
		if overLimit {
			// If limit is exceeded, execute the reject strategy.
			return l.onReject(ctx, req, info, handler)
		}
		// Otherwise, proceed with the normal flow.
		return handler(ctx, req)
	}
}

// limit checks whether the number of requests has exceeded the rate limit.
// It returns true if the limit has been exceeded, false otherwise.
func (l *RedisSlideWindowLimiter) limit(ctx context.Context) (bool, error) {
	return l.client.Eval(ctx, luaSlideWindow, []string{l.service}, l.interval.Milliseconds(), l.rate, time.Now().UnixMilli()).Bool()
}
