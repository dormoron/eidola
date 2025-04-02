package mfa

import (
	"context"
	"fmt"
	"time"

	"github.com/dormoron/eidola/auth/redis"
)

// RedisMFARepository Redis MFA仓库实现
type RedisMFARepository struct {
	// Redis客户端
	client redis.Client
	// 键前缀
	keyPrefix string
	// 过期时间
	expiration time.Duration
}

// RedisMFARepositoryConfig Redis MFA仓库配置
type RedisMFARepositoryConfig struct {
	// Redis客户端
	Client redis.Client
	// 键前缀
	KeyPrefix string
	// 数据有效期
	Expiration time.Duration
}

// NewRedisMFARepository 创建Redis MFA仓库
func NewRedisMFARepository(config RedisMFARepositoryConfig) (*RedisMFARepository, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("Redis客户端不能为空")
	}

	if config.KeyPrefix == "" {
		config.KeyPrefix = "mfa:"
	}

	if config.Expiration <= 0 {
		config.Expiration = time.Hour * 24 * 365 // 默认一年有效期
	}

	return &RedisMFARepository{
		client:     config.Client,
		keyPrefix:  config.KeyPrefix,
		expiration: config.Expiration,
	}, nil
}

// GetUserMFA 实现MFARepository.GetUserMFA
func (r *RedisMFARepository) GetUserMFA(ctx context.Context, userID string, mfaType MFAType) (map[string]string, error) {
	key := fmt.Sprintf("%suser:%s:%s", r.keyPrefix, userID, mfaType)

	// 获取用户MFA设置
	data, err := r.client.HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}

	// 判断数据是否存在
	if len(data) == 0 {
		return nil, ErrMFANotEnabled
	}

	return data, nil
}

// SetUserMFA 实现MFARepository.SetUserMFA
func (r *RedisMFARepository) SetUserMFA(ctx context.Context, userID string, mfaType MFAType, data map[string]string) error {
	key := fmt.Sprintf("%suser:%s:%s", r.keyPrefix, userID, mfaType)

	// 存储MFA设置
	for field, value := range data {
		err := r.client.HSet(ctx, key, field, value)
		if err != nil {
			return err
		}
	}

	// 设置有效期
	err := r.client.Expire(ctx, key, r.expiration)
	if err != nil {
		return err
	}

	// 添加到用户类型映射
	userTypesKey := fmt.Sprintf("%suser_types:%s", r.keyPrefix, userID)
	err = r.client.HSet(ctx, userTypesKey, string(mfaType), "1")
	if err != nil {
		return err
	}

	// 设置用户类型映射的有效期
	return r.client.Expire(ctx, userTypesKey, r.expiration)
}

// DisableUserMFA 实现MFARepository.DisableUserMFA
func (r *RedisMFARepository) DisableUserMFA(ctx context.Context, userID string, mfaType MFAType) error {
	key := fmt.Sprintf("%suser:%s:%s", r.keyPrefix, userID, mfaType)

	// 删除MFA设置
	err := r.client.Del(ctx, key)
	if err != nil {
		return err
	}

	// 从用户类型映射中移除
	userTypesKey := fmt.Sprintf("%suser_types:%s", r.keyPrefix, userID)
	return r.client.HDel(ctx, userTypesKey, string(mfaType))
}

// ListUserMFA 实现MFARepository.ListUserMFA
func (r *RedisMFARepository) ListUserMFA(ctx context.Context, userID string) ([]MFAType, error) {
	userTypesKey := fmt.Sprintf("%suser_types:%s", r.keyPrefix, userID)

	// 获取用户启用的MFA类型
	data, err := r.client.HGetAll(ctx, userTypesKey)
	if err != nil {
		return nil, err
	}

	// 判断数据是否存在
	if len(data) == 0 {
		return []MFAType{}, nil
	}

	var types []MFAType
	for t := range data {
		types = append(types, MFAType(t))
	}

	return types, nil
}

// 以下是Redis客户端实现示例

/*
import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

// GoRedisMFAClient 使用go-redis库实现RedisMFAClient接口
type GoRedisMFAClient struct {
	client *redis.Client
}

// NewGoRedisMFAClient 创建go-redis客户端
func NewGoRedisMFAClient(addr, password string, db int) (*GoRedisMFAClient, error) {
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

	return &GoRedisMFAClient{
		client: client,
	}, nil
}

// Get 实现RedisMFAClient.Get
func (c *GoRedisMFAClient) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// Set 实现RedisMFAClient.Set
func (c *GoRedisMFAClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(ctx, key, value, expiration).Err()
}

// Del 实现RedisMFAClient.Del
func (c *GoRedisMFAClient) Del(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

// Keys 实现RedisMFAClient.Keys
func (c *GoRedisMFAClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.client.Keys(ctx, pattern).Result()
}

// HSet 实现RedisMFAClient.HSet
func (c *GoRedisMFAClient) HSet(ctx context.Context, key string, field string, value interface{}) error {
	return c.client.HSet(ctx, key, field, value).Err()
}

// HGet 实现RedisMFAClient.HGet
func (c *GoRedisMFAClient) HGet(ctx context.Context, key string, field string) (string, error) {
	return c.client.HGet(ctx, key, field).Result()
}

// HGetAll 实现RedisMFAClient.HGetAll
func (c *GoRedisMFAClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

// HDel 实现RedisMFAClient.HDel
func (c *GoRedisMFAClient) HDel(ctx context.Context, key string, fields ...string) error {
	return c.client.HDel(ctx, key, fields...).Err()
}

// Expire 实现RedisMFAClient.Expire
func (c *GoRedisMFAClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, key, expiration).Err()
}

// Close 实现RedisMFAClient.Close
func (c *GoRedisMFAClient) Close() error {
	return c.client.Close()
}
*/
