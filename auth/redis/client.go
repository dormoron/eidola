package redis

import (
	"context"
	"time"
)

// Client 是通用的Redis客户端接口
// 用于在整个auth包中使用
type Client interface {
	// Get 获取值
	Get(ctx context.Context, key string) (string, error)
	// Set 设置键值对
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	// Del 删除键
	Del(ctx context.Context, keys ...string) error
	// Keys 查找匹配的键
	Keys(ctx context.Context, pattern string) ([]string, error)
	// HSet 设置哈希字段
	HSet(ctx context.Context, key string, field string, value interface{}) error
	// HGet 获取哈希字段
	HGet(ctx context.Context, key string, field string) (string, error)
	// HGetAll 获取所有哈希字段
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	// HDel 删除哈希字段
	HDel(ctx context.Context, key string, fields ...string) error
	// Expire 设置过期时间
	Expire(ctx context.Context, key string, expiration time.Duration) error
	// TTL 获取剩余时间
	TTL(ctx context.Context, key string) (time.Duration, error)
	// Close 关闭连接
	Close() error
}
