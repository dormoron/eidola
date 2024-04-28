package ratelimit

import (
	"context"
	_ "embed"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"time"
)

//go:embed lua/fix_window.lua
var luaFixWindow string

// RedisFixWindowLimiterOptions defines the function signature for configuration options for RedisFixWindowLimiter.
type RedisFixWindowLimiterOptions func(l *RedisFixWindowLimiter)

// RedisFixWindowLimiter implements rate limiting using a fixed window approach with Redis.
type RedisFixWindowLimiter struct {
	client   redis.Cmdable  // Client is the Redis client used for rate limiting.
	service  string         // Service is the identifier for the service being limited.
	interval time.Duration  // Interval specifies the width of the fixed window.
	rate     int            // Rate specifies the maximum number of requests allowed within the interval.
	onReject rejectStrategy // onReject is a strategy to be executed when a request is rejected.
}

// NewRedisFixWindowLimiter returns a new instance of RedisFixWindowLimiter with the provided configuration.
func NewRedisFixWindowLimiter(client redis.Cmdable, service string, interval time.Duration, rate int) *RedisFixWindowLimiter {
	return &RedisFixWindowLimiter{
		client:   client,
		service:  service,
		interval: interval,
		rate:     rate,
		onReject: defaultRejectStrategy, // Set default reject strategy on new RedisFixWindowLimiter.
	}
}

// RedisFixWindowMarkFailed provides an option to set the markFailedStrategy as the reject strategy.
func RedisFixWindowMarkFailed() RedisFixWindowLimiterOptions {
	return func(l *RedisFixWindowLimiter) {
		l.onReject = markFailedStrategy
	}
}

// LimitUnary returns a grpc.UnaryServerInterceptor that imposes rate limiting based on the fixed window mechanism.
func (l *RedisFixWindowLimiter) LimitUnary() grpc.UnaryServerInterceptor {
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
func (l *RedisFixWindowLimiter) limit(ctx context.Context) (bool, error) {
	return l.client.Eval(ctx, luaFixWindow, []string{l.service}, l.interval.Milliseconds(), l.rate).Bool()
}
