package ratelimit

import (
	"context"
	_ "embed"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"time"
)

type RedisFixWindowLimiterOptions func(l *RedisFixWindowLimiter)

//go:embed lua/fix_window.lua
var luaFixWindow string

type RedisFixWindowLimiter struct {
	client   redis.Cmdable
	service  string
	interval time.Duration
	rate     int
	onReject rejectStrategy
}

func NewRedisFixWindowLimiter(client redis.Cmdable, service string,
	interval time.Duration, rate int) *RedisFixWindowLimiter {
	return &RedisFixWindowLimiter{
		client:   client,
		service:  service,
		interval: interval,
		rate:     rate,
		onReject: defaultRejectStrategy,
	}
}

func RedisFixWindowMarkFailed() RedisFixWindowLimiterOptions {
	return func(l *RedisFixWindowLimiter) {
		l.onReject = markFailedStrategy
	}
}

func (l *RedisFixWindowLimiter) BuildServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		limit, err := l.limit(ctx)
		if err != nil {
			return
		}
		if limit {
			return l.onReject(ctx, req, info, handler)
		}
		resp, err = handler(ctx, req)
		return
	}
}

func (l *RedisFixWindowLimiter) limit(ctx context.Context) (bool, error) {
	return l.client.Eval(ctx, luaFixWindow, []string{l.service},
		l.interval.Milliseconds(), l.rate).Bool()
}
