package token

import (
	"context"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth/redis"
)

// TokenBlacklist 定义令牌黑名单接口
type TokenBlacklist interface {
	// AddToBlacklist 将令牌添加到黑名单
	AddToBlacklist(ctx context.Context, tokenID string, expiration time.Duration) error
	// IsBlacklisted 检查令牌是否在黑名单中
	IsBlacklisted(ctx context.Context, tokenID string) (bool, error)
	// RemoveFromBlacklist 从黑名单中移除令牌（主要用于测试）
	RemoveFromBlacklist(ctx context.Context, tokenID string) error
	// CleanupExpired 清理过期的黑名单条目
	CleanupExpired(ctx context.Context) error
	// Close 关闭黑名单
	Close() error
}

// InMemoryBlacklist 内存黑名单实现
type InMemoryBlacklist struct {
	// 黑名单映射
	blacklist map[string]int64
	// 互斥锁
	mu sync.RWMutex
}

// NewInMemoryBlacklist 创建内存黑名单
func NewInMemoryBlacklist() *InMemoryBlacklist {
	return &InMemoryBlacklist{
		blacklist: make(map[string]int64),
	}
}

// AddToBlacklist 将令牌添加到黑名单
func (b *InMemoryBlacklist) AddToBlacklist(ctx context.Context, tokenID string, expiration time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	expiryTime := time.Now().Add(expiration).Unix()
	b.blacklist[tokenID] = expiryTime

	return nil
}

// IsBlacklisted 检查令牌是否在黑名单中
func (b *InMemoryBlacklist) IsBlacklisted(ctx context.Context, tokenID string) (bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	expiryTime, exists := b.blacklist[tokenID]
	if !exists {
		return false, nil
	}

	// 检查是否已过期
	now := time.Now().Unix()
	if expiryTime < now {
		// 自动从黑名单中移除
		delete(b.blacklist, tokenID)
		return false, nil
	}

	return true, nil
}

// RemoveFromBlacklist 从黑名单中移除令牌
func (b *InMemoryBlacklist) RemoveFromBlacklist(ctx context.Context, tokenID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.blacklist, tokenID)

	return nil
}

// CleanupExpired 清理过期的黑名单条目
func (b *InMemoryBlacklist) CleanupExpired(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now().Unix()
	for tokenID, expiryTime := range b.blacklist {
		if expiryTime < now {
			delete(b.blacklist, tokenID)
		}
	}

	return nil
}

// Close 关闭黑名单
func (b *InMemoryBlacklist) Close() error {
	// 内存黑名单无需特殊关闭操作
	return nil
}

// RedisBlacklist Redis黑名单实现
type RedisBlacklist struct {
	// Redis客户端
	client redis.Client
	// 键前缀
	keyPrefix string
}

// NewRedisBlacklist 创建Redis黑名单
func NewRedisBlacklist(client redis.Client, keyPrefix string) *RedisBlacklist {
	if keyPrefix == "" {
		keyPrefix = "blacklist:"
	}

	return &RedisBlacklist{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// AddToBlacklist 将令牌添加到黑名单
func (b *RedisBlacklist) AddToBlacklist(ctx context.Context, tokenID string, expiration time.Duration) error {
	key := b.keyPrefix + tokenID
	return b.client.Set(ctx, key, "1", expiration)
}

// IsBlacklisted 检查令牌是否在黑名单中
func (b *RedisBlacklist) IsBlacklisted(ctx context.Context, tokenID string) (bool, error) {
	key := b.keyPrefix + tokenID
	_, err := b.client.Get(ctx, key)
	if err != nil {
		if err.Error() == "redis: nil" { // 键不存在
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RemoveFromBlacklist 从黑名单中移除令牌
func (b *RedisBlacklist) RemoveFromBlacklist(ctx context.Context, tokenID string) error {
	key := b.keyPrefix + tokenID
	return b.client.Del(ctx, key)
}

// CleanupExpired 清理过期的黑名单条目
func (b *RedisBlacklist) CleanupExpired(ctx context.Context) error {
	// Redis会自动清理过期键，无需额外操作
	return nil
}

// Close 关闭黑名单
func (b *RedisBlacklist) Close() error {
	return b.client.Close()
}
