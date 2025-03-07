package jwt

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth"
	jwt "github.com/golang-jwt/jwt/v5"
)

var (
	// ErrTokenBlacklisted 令牌已经被列入黑名单
	ErrTokenBlacklisted = errors.New("token is blacklisted")
)

// BlacklistStorage 黑名单存储接口
type BlacklistStorage interface {
	// Add 将令牌添加到黑名单
	Add(ctx context.Context, token string, expiry time.Time) error

	// Contains 检查令牌是否在黑名单中
	Contains(ctx context.Context, token string) (bool, error)

	// Remove 从黑名单中移除令牌（通常用于过期的令牌）
	Remove(ctx context.Context, token string) error

	// Cleanup 清理过期的黑名单条目
	Cleanup(ctx context.Context) error

	// Close 关闭存储
	Close() error
}

// MemoryBlacklistStorage 内存黑名单存储
type MemoryBlacklistStorage struct {
	// 黑名单映射，键为令牌，值为过期时间
	blacklist map[string]time.Time
	// 保护共享资源的锁
	mu sync.RWMutex
	// 清理间隔
	cleanupInterval time.Duration
	// 通知停止清理goroutine
	stopCh chan struct{}
}

// NewMemoryBlacklistStorage 创建新的内存黑名单存储
func NewMemoryBlacklistStorage(cleanupInterval time.Duration) *MemoryBlacklistStorage {
	storage := &MemoryBlacklistStorage{
		blacklist:       make(map[string]time.Time),
		cleanupInterval: cleanupInterval,
		stopCh:          make(chan struct{}),
	}

	// 启动自动清理过期令牌的goroutine
	go storage.cleanupLoop()

	return storage
}

// Add 将令牌添加到黑名单
func (s *MemoryBlacklistStorage) Add(ctx context.Context, token string, expiry time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.blacklist[token] = expiry
	return nil
}

// Contains 检查令牌是否在黑名单中
func (s *MemoryBlacklistStorage) Contains(ctx context.Context, token string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	expiry, exists := s.blacklist[token]
	if !exists {
		return false, nil
	}

	// 如果令牌已过期，从黑名单中移除
	if time.Now().After(expiry) {
		// 在读锁中不能修改map，需要在cleanupLoop中处理
		return false, nil
	}

	return true, nil
}

// Remove 从黑名单中移除令牌
func (s *MemoryBlacklistStorage) Remove(ctx context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.blacklist, token)
	return nil
}

// Cleanup 清理过期的黑名单条目
func (s *MemoryBlacklistStorage) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for token, expiry := range s.blacklist {
		if now.After(expiry) {
			delete(s.blacklist, token)
		}
	}

	return nil
}

// Close 关闭存储
func (s *MemoryBlacklistStorage) Close() error {
	close(s.stopCh)
	return nil
}

// cleanupLoop 定期清理过期的黑名单条目
func (s *MemoryBlacklistStorage) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = s.Cleanup(context.Background())
		case <-s.stopCh:
			return
		}
	}
}

// RedisBlacklistStorage Redis黑名单存储
// 这里只提供接口定义，实际实现可以根据项目使用的Redis客户端库调整
type RedisBlacklistStorage struct {
	// Redis客户端
	client interface{} // 可以使用redis.Client或其他客户端
	// 键前缀
	keyPrefix string
	// 清理间隔
	cleanupInterval time.Duration
	// 通知停止清理goroutine
	stopCh chan struct{}
}

// TokenBlacklist 令牌黑名单
type TokenBlacklist struct {
	// 令牌管理器
	tokenManager *JWTManager
	// 黑名单存储
	storage BlacklistStorage
}

// NewTokenBlacklist 创建新的令牌黑名单
func NewTokenBlacklist(tokenManager *JWTManager, storage BlacklistStorage) *TokenBlacklist {
	return &TokenBlacklist{
		tokenManager: tokenManager,
		storage:      storage,
	}
}

// Add 将令牌添加到黑名单
func (b *TokenBlacklist) Add(ctx context.Context, token string) error {
	// 验证令牌，获取过期时间
	claims, err := b.tokenManager.ValidateToken(token)
	if err != nil {
		return err
	}

	// 从claims中获取exp（过期时间）
	var expTime time.Time
	exp, ok := claims["exp"]
	if !ok {
		return errors.New("missing expiry claim in token")
	}

	// 根据类型解析exp
	switch v := exp.(type) {
	case float64:
		// JWT库可能将exp解析为float64
		expTime = time.Unix(int64(v), 0)
	case *jwt.NumericDate:
		expTime = v.Time
	case time.Time:
		expTime = v
	default:
		return errors.New("invalid token expiry format")
	}

	// 将令牌添加到黑名单
	return b.storage.Add(ctx, token, expTime)
}

// Contains 检查令牌是否在黑名单中
func (b *TokenBlacklist) Contains(ctx context.Context, token string) (bool, error) {
	return b.storage.Contains(ctx, token)
}

// IsTokenValid 检查令牌是否有效（不在黑名单中且验证通过）
func (b *TokenBlacklist) IsTokenValid(ctx context.Context, token string) (bool, auth.Claims, error) {
	// 首先检查令牌是否在黑名单中
	blacklisted, err := b.Contains(ctx, token)
	if err != nil {
		return false, nil, err
	}

	if blacklisted {
		return false, nil, ErrTokenBlacklisted
	}

	// 验证令牌
	claims, err := b.tokenManager.ValidateToken(token)
	if err != nil {
		return false, nil, err
	}

	return true, claims, nil
}

// Close 关闭黑名单
func (b *TokenBlacklist) Close() error {
	return b.storage.Close()
}

// BlacklistedJWTAuthenticator 带黑名单的JWT认证器
type BlacklistedJWTAuthenticator struct {
	tokenManager *JWTManager
	blacklist    *TokenBlacklist
}

// NewBlacklistedJWTAuthenticator 创建带黑名单的JWT认证器
func NewBlacklistedJWTAuthenticator(tokenManager *JWTManager, blacklist *TokenBlacklist) *BlacklistedJWTAuthenticator {
	return &BlacklistedJWTAuthenticator{
		tokenManager: tokenManager,
		blacklist:    blacklist,
	}
}

// Authenticate 验证凭证
func (a *BlacklistedJWTAuthenticator) Authenticate(ctx context.Context, creds auth.Credentials) (*auth.User, error) {
	token := creds.GetToken()

	// 检查令牌是否有效（不在黑名单中且验证通过）
	valid, claims, err := a.blacklist.IsTokenValid(ctx, token)
	if err != nil {
		if errors.Is(err, ErrTokenBlacklisted) {
			return nil, auth.ErrUnauthenticated
		}
		return nil, err
	}

	if !valid {
		return nil, auth.ErrUnauthenticated
	}

	// 从载荷中提取用户信息
	userID, _ := claims["uid"].(string)
	username, _ := claims["username"].(string)
	roles, _ := claims["roles"].([]string)
	permissions, _ := claims["permissions"].([]string)

	// 创建用户
	user := &auth.User{
		ID:          userID,
		Name:        username,
		Roles:       roles,
		Permissions: permissions,
		Metadata:    make(map[string]interface{}),
	}

	// 复制元数据
	for k, v := range claims {
		if k != "uid" && k != "username" && k != "roles" && k != "permissions" &&
			k != "exp" && k != "iat" && k != "nbf" && k != "iss" && k != "aud" {
			user.Metadata[k] = v
		}
	}

	// 将载荷添加到上下文
	ctx = auth.WithClaims(ctx, claims)

	return user, nil
}
