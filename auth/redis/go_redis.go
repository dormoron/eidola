package redis

import (
	"context"
	"time"
)

// GoRedisClient 是使用go-redis库的Redis客户端实现
type GoRedisClient struct {
	client interface {
		Get(ctx context.Context, key string) interface{ Result() (string, error) }
		Set(ctx context.Context, key string, value interface{}, expiration time.Duration) interface{ Err() error }
		Del(ctx context.Context, keys ...string) interface{ Err() error }
		Keys(ctx context.Context, pattern string) interface{ Result() ([]string, error) }
		HSet(ctx context.Context, key string, values ...interface{}) interface{ Err() error }
		HGet(ctx context.Context, key, field string) interface{ Result() (string, error) }
		HGetAll(ctx context.Context, key string) interface {
			Result() (map[string]string, error)
		}
		HDel(ctx context.Context, key string, fields ...string) interface{ Err() error }
		Expire(ctx context.Context, key string, expiration time.Duration) interface{ Err() error }
		TTL(ctx context.Context, key string) interface{ Result() (time.Duration, error) }
		Ping(ctx context.Context) interface{ Result() (string, error) }
		Close() error
	}
}

// NewGoRedisClient 创建go-redis客户端的实例
// 参数client必须是*github.com/go-redis/redis/v8.Client或兼容接口
func NewGoRedisClient(client interface{}) (*GoRedisClient, error) {
	if client == nil {
		return nil, ErrNilRedisClient
	}
	return &GoRedisClient{client: client.(interface {
		Get(ctx context.Context, key string) interface{ Result() (string, error) }
		Set(ctx context.Context, key string, value interface{}, expiration time.Duration) interface{ Err() error }
		Del(ctx context.Context, keys ...string) interface{ Err() error }
		Keys(ctx context.Context, pattern string) interface{ Result() ([]string, error) }
		HSet(ctx context.Context, key string, values ...interface{}) interface{ Err() error }
		HGet(ctx context.Context, key, field string) interface{ Result() (string, error) }
		HGetAll(ctx context.Context, key string) interface {
			Result() (map[string]string, error)
		}
		HDel(ctx context.Context, key string, fields ...string) interface{ Err() error }
		Expire(ctx context.Context, key string, expiration time.Duration) interface{ Err() error }
		TTL(ctx context.Context, key string) interface{ Result() (time.Duration, error) }
		Ping(ctx context.Context) interface{ Result() (string, error) }
		Close() error
	})}, nil
}

// Get 实现Client.Get
func (c *GoRedisClient) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// Set 实现Client.Set
func (c *GoRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(ctx, key, value, expiration).Err()
}

// Del 实现Client.Del
func (c *GoRedisClient) Del(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

// Keys 实现Client.Keys
func (c *GoRedisClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.client.Keys(ctx, pattern).Result()
}

// HSet 实现Client.HSet
func (c *GoRedisClient) HSet(ctx context.Context, key string, field string, value interface{}) error {
	return c.client.HSet(ctx, key, field, value).Err()
}

// HGet 实现Client.HGet
func (c *GoRedisClient) HGet(ctx context.Context, key string, field string) (string, error) {
	return c.client.HGet(ctx, key, field).Result()
}

// HGetAll 实现Client.HGetAll
func (c *GoRedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

// HDel 实现Client.HDel
func (c *GoRedisClient) HDel(ctx context.Context, key string, fields ...string) error {
	return c.client.HDel(ctx, key, fields...).Err()
}

// Expire 实现Client.Expire
func (c *GoRedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, key, expiration).Err()
}

// TTL 实现Client.TTL
func (c *GoRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.client.TTL(ctx, key).Result()
}

// Close 实现Client.Close
func (c *GoRedisClient) Close() error {
	return c.client.Close()
}
