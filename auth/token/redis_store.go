package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dormoron/eidola/auth"
	"github.com/dormoron/eidola/auth/redis"
)

// RedisTokenStore Redis令牌存储实现
type RedisTokenStore struct {
	// Redis客户端
	client redis.Client
	// 键前缀
	keyPrefix string
	// 过期时间
	expiration time.Duration
	// 用户缓存
	userCache UserCache
	// 是否启用缓存
	enableCache bool
}

// UserCache 提供用户缓存功能
type UserCache interface {
	// GetUser 获取用户
	GetUser(userID string) (*auth.User, bool)
	// SetUser 设置用户
	SetUser(user *auth.User)
	// DelUser 删除用户
	DelUser(userID string)
}

// SimpleUserCache 简单的内存用户缓存实现
type SimpleUserCache struct {
	users map[string]*auth.User
	mu    map[string]bool
}

// NewSimpleUserCache 创建简单用户缓存
func NewSimpleUserCache() *SimpleUserCache {
	return &SimpleUserCache{
		users: make(map[string]*auth.User),
		mu:    make(map[string]bool),
	}
}

// GetUser 获取用户
func (c *SimpleUserCache) GetUser(userID string) (*auth.User, bool) {
	user, ok := c.users[userID]
	return user, ok
}

// SetUser 设置用户
func (c *SimpleUserCache) SetUser(user *auth.User) {
	c.users[user.ID] = user
}

// DelUser 删除用户
func (c *SimpleUserCache) DelUser(userID string) {
	delete(c.users, userID)
}

// RedisTokenStoreConfig Redis令牌存储配置
type RedisTokenStoreConfig struct {
	// Redis客户端
	Client redis.Client
	// 键前缀
	KeyPrefix string
	// 令牌过期时间
	Expiration time.Duration
	// 用户缓存
	UserCache UserCache
	// 是否启用缓存
	EnableCache bool
}

// NewRedisTokenStore 创建Redis令牌存储
func NewRedisTokenStore(config RedisTokenStoreConfig) (*RedisTokenStore, error) {
	if config.Client == nil {
		return nil, errors.New("Redis客户端不能为空")
	}

	if config.KeyPrefix == "" {
		config.KeyPrefix = "token:"
	}

	if config.Expiration <= 0 {
		config.Expiration = time.Hour * 24 * 7 // 默认7天
	}

	if config.EnableCache && config.UserCache == nil {
		config.UserCache = NewSimpleUserCache()
	}

	return &RedisTokenStore{
		client:      config.Client,
		keyPrefix:   config.KeyPrefix,
		expiration:  config.Expiration,
		userCache:   config.UserCache,
		enableCache: config.EnableCache,
	}, nil
}

// StoreToken 存储令牌
func (s *RedisTokenStore) StoreToken(ctx context.Context, token *TokenData) error {
	// 设置创建时间
	if token.CreatedAt == 0 {
		token.CreatedAt = time.Now().Unix()
	}

	// 序列化令牌数据
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("序列化令牌数据失败: %w", err)
	}

	// 计算过期时间
	expiration := time.Until(time.Unix(token.ExpiresAt, 0))
	if expiration <= 0 {
		expiration = s.expiration
	}

	// 存储令牌
	tokenKey := s.keyPrefix + "id:" + token.TokenID
	err = s.client.Set(ctx, tokenKey, string(tokenJSON), expiration)
	if err != nil {
		return err
	}

	// 存储刷新令牌映射
	if token.RefreshToken != "" {
		refreshKey := s.keyPrefix + "refresh:" + token.RefreshToken
		err = s.client.Set(ctx, refreshKey, token.TokenID, expiration)
		if err != nil {
			return err
		}
	}

	// 存储用户映射
	userTokensKey := s.keyPrefix + "user:" + token.UserID
	err = s.client.HSet(ctx, userTokensKey, token.TokenID, "1")
	if err != nil {
		return err
	}

	// 设置用户映射过期时间
	err = s.client.Expire(ctx, userTokensKey, s.expiration)
	if err != nil {
		return err
	}

	return nil
}

// GetToken 获取令牌
func (s *RedisTokenStore) GetToken(ctx context.Context, tokenID string) (*TokenData, error) {
	tokenKey := s.keyPrefix + "id:" + tokenID
	tokenJSON, err := s.client.Get(ctx, tokenKey)
	if err != nil {
		return nil, err
	}

	var token TokenData
	err = json.Unmarshal([]byte(tokenJSON), &token)
	if err != nil {
		return nil, fmt.Errorf("解析令牌数据失败: %w", err)
	}

	return &token, nil
}

// GetTokenByRefresh 通过刷新令牌获取令牌
func (s *RedisTokenStore) GetTokenByRefresh(ctx context.Context, refreshToken string) (*TokenData, error) {
	refreshKey := s.keyPrefix + "refresh:" + refreshToken
	tokenID, err := s.client.Get(ctx, refreshKey)
	if err != nil {
		return nil, err
	}

	return s.GetToken(ctx, tokenID)
}

// RevokeToken 撤销令牌
func (s *RedisTokenStore) RevokeToken(ctx context.Context, tokenID string) error {
	// 获取令牌
	token, err := s.GetToken(ctx, tokenID)
	if err != nil {
		return err
	}

	// 删除令牌
	tokenKey := s.keyPrefix + "id:" + tokenID
	err = s.client.Del(ctx, tokenKey)
	if err != nil {
		return err
	}

	// 删除刷新令牌映射
	if token.RefreshToken != "" {
		refreshKey := s.keyPrefix + "refresh:" + token.RefreshToken
		err = s.client.Del(ctx, refreshKey)
		if err != nil {
			return err
		}
	}

	// 从用户映射中移除
	userTokensKey := s.keyPrefix + "user:" + token.UserID
	err = s.client.HDel(ctx, userTokensKey, tokenID)
	if err != nil {
		return err
	}

	return nil
}

// IsTokenValid 检查令牌是否有效
func (s *RedisTokenStore) IsTokenValid(ctx context.Context, tokenID string) (bool, error) {
	tokenKey := s.keyPrefix + "id:" + tokenID
	tokenJSON, err := s.client.Get(ctx, tokenKey)
	if err != nil {
		// 令牌不存在
		if err.Error() == "redis: nil" {
			return false, nil
		}
		return false, err
	}

	var token TokenData
	err = json.Unmarshal([]byte(tokenJSON), &token)
	if err != nil {
		return false, fmt.Errorf("解析令牌数据失败: %w", err)
	}

	// 检查令牌是否已过期
	now := time.Now().Unix()
	if token.ExpiresAt < now {
		return false, nil
	}

	return true, nil
}

// CleanupExpiredTokens 清理过期的令牌
func (s *RedisTokenStore) CleanupExpiredTokens(ctx context.Context) error {
	// Redis会自动清理过期键，无需额外操作
	return nil
}

// GetUserByID 获取用户
func (s *RedisTokenStore) GetUserByID(ctx context.Context, userID string) (*auth.User, error) {
	if s.enableCache {
		// 尝试从缓存获取
		if user, ok := s.userCache.GetUser(userID); ok {
			return user, nil
		}
	}

	// 从Redis获取用户
	userKey := s.keyPrefix + "user_data:" + userID
	userJSON, err := s.client.Get(ctx, userKey)
	if err != nil {
		return nil, err
	}

	var user auth.User
	err = json.Unmarshal([]byte(userJSON), &user)
	if err != nil {
		return nil, fmt.Errorf("解析用户数据失败: %w", err)
	}

	// 写入缓存
	if s.enableCache {
		s.userCache.SetUser(&user)
	}

	return &user, nil
}

// StoreUser 存储用户
func (s *RedisTokenStore) StoreUser(ctx context.Context, user *auth.User) error {
	// 序列化用户数据
	userJSON, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("序列化用户数据失败: %w", err)
	}

	// 存储用户
	userKey := s.keyPrefix + "user_data:" + user.ID
	err = s.client.Set(ctx, userKey, string(userJSON), s.expiration)
	if err != nil {
		return err
	}

	// 写入缓存
	if s.enableCache {
		s.userCache.SetUser(user)
	}

	return nil
}

// DeleteUser 删除用户
func (s *RedisTokenStore) DeleteUser(ctx context.Context, userID string) error {
	// 获取用户的令牌列表
	userTokensKey := s.keyPrefix + "user:" + userID
	tokens, err := s.client.HGetAll(ctx, userTokensKey)
	if err != nil {
		return err
	}

	// 删除每个令牌
	for tokenID := range tokens {
		// 获取令牌
		token, err := s.GetToken(ctx, tokenID)
		if err != nil {
			continue
		}

		// 删除令牌
		tokenKey := s.keyPrefix + "id:" + tokenID
		err = s.client.Del(ctx, tokenKey)
		if err != nil {
			return err
		}

		// 删除刷新令牌映射
		if token.RefreshToken != "" {
			refreshKey := s.keyPrefix + "refresh:" + token.RefreshToken
			err = s.client.Del(ctx, refreshKey)
			if err != nil {
				return err
			}
		}
	}

	// 删除用户的令牌映射
	err = s.client.Del(ctx, userTokensKey)
	if err != nil {
		return err
	}

	// 删除用户数据
	userKey := s.keyPrefix + "user_data:" + userID
	err = s.client.Del(ctx, userKey)
	if err != nil {
		return err
	}

	// 从缓存中删除
	if s.enableCache {
		s.userCache.DelUser(userID)
	}

	return nil
}

// Close 关闭令牌存储
func (s *RedisTokenStore) Close() error {
	return s.client.Close()
}

// 以下是Redis客户端实现示例，需要根据实际使用的Redis库适配

/*
import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

// GoRedisClient 使用go-redis库实现RedisClient接口
type GoRedisClient struct {
	client *redis.Client
}

// NewGoRedisClient 创建go-redis客户端
func NewGoRedisClient(addr, password string, db int) (*GoRedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// 测试连接
	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		return nil, err
	}

	return &GoRedisClient{
		client: client,
	}, nil
}

// Set 实现RedisClient.Set
func (c *GoRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(ctx, key, value, expiration).Err()
}

// Get 实现RedisClient.Get
func (c *GoRedisClient) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// Del 实现RedisClient.Del
func (c *GoRedisClient) Del(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

// Keys 实现RedisClient.Keys
func (c *GoRedisClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.client.Keys(ctx, pattern).Result()
}

// HSet 实现RedisClient.HSet
func (c *GoRedisClient) HSet(ctx context.Context, key string, field string, value interface{}) error {
	return c.client.HSet(ctx, key, field, value).Err()
}

// HGet 实现RedisClient.HGet
func (c *GoRedisClient) HGet(ctx context.Context, key string, field string) (string, error) {
	return c.client.HGet(ctx, key, field).Result()
}

// HGetAll 实现RedisClient.HGetAll
func (c *GoRedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

// HDel 实现RedisClient.HDel
func (c *GoRedisClient) HDel(ctx context.Context, key string, fields ...string) error {
	return c.client.HDel(ctx, key, fields...).Err()
}

// Expire 实现RedisClient.Expire
func (c *GoRedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, key, expiration).Err()
}

// TTL 实现RedisClient.TTL
func (c *GoRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.client.TTL(ctx, key).Result()
}

// Close 实现RedisClient.Close
func (c *GoRedisClient) Close() error {
	return c.client.Close()
}
*/
