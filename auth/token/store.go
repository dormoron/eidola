package token

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth"
)

// TokenData 表示存储的令牌数据
type TokenData struct {
	// 访问令牌
	AccessToken string
	// 刷新令牌
	RefreshToken string
	// 用户ID
	UserID string
	// 过期时间
	ExpiresAt int64
	// 令牌ID
	TokenID string
	// 是否被撤销
	Revoked bool
	// 创建时间
	CreatedAt int64
}

// TokenStore 定义令牌存储接口
type TokenStore interface {
	// StoreToken 存储令牌
	StoreToken(ctx context.Context, token *TokenData) error
	// GetToken 获取令牌
	GetToken(ctx context.Context, tokenID string) (*TokenData, error)
	// GetTokenByRefresh 通过刷新令牌获取令牌
	GetTokenByRefresh(ctx context.Context, refreshToken string) (*TokenData, error)
	// RevokeToken 撤销令牌
	RevokeToken(ctx context.Context, tokenID string) error
	// IsTokenValid 检查令牌是否有效
	IsTokenValid(ctx context.Context, tokenID string) (bool, error)
	// CleanupExpiredTokens 清理过期令牌
	CleanupExpiredTokens(ctx context.Context) error
	// GetUserByID 获取用户信息
	GetUserByID(ctx context.Context, userID string) (*auth.User, error)
	// Close 关闭存储
	Close() error
}

// InMemoryTokenStore 内存令牌存储实现
type InMemoryTokenStore struct {
	// 令牌映射
	tokens map[string]*TokenData
	// 刷新令牌映射到令牌ID
	refreshTokens map[string]string
	// 用户映射
	users map[string]*auth.User
	// 互斥锁
	mu sync.RWMutex
}

// NewInMemoryTokenStore 创建内存令牌存储
func NewInMemoryTokenStore() *InMemoryTokenStore {
	return &InMemoryTokenStore{
		tokens:        make(map[string]*TokenData),
		refreshTokens: make(map[string]string),
		users:         make(map[string]*auth.User),
	}
}

// StoreToken 存储令牌
func (s *InMemoryTokenStore) StoreToken(ctx context.Context, token *TokenData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 设置创建时间
	if token.CreatedAt == 0 {
		token.CreatedAt = time.Now().Unix()
	}

	// 存储令牌
	s.tokens[token.TokenID] = token

	// 存储刷新令牌映射
	if token.RefreshToken != "" {
		s.refreshTokens[token.RefreshToken] = token.TokenID
	}

	return nil
}

// GetToken 获取令牌
func (s *InMemoryTokenStore) GetToken(ctx context.Context, tokenID string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token, ok := s.tokens[tokenID]
	if !ok {
		return nil, errors.New("令牌未找到")
	}

	return token, nil
}

// GetTokenByRefresh 通过刷新令牌获取令牌
func (s *InMemoryTokenStore) GetTokenByRefresh(ctx context.Context, refreshToken string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokenID, ok := s.refreshTokens[refreshToken]
	if !ok {
		return nil, errors.New("刷新令牌未找到")
	}

	token, ok := s.tokens[tokenID]
	if !ok {
		return nil, errors.New("令牌未找到")
	}

	return token, nil
}

// RevokeToken 撤销令牌
func (s *InMemoryTokenStore) RevokeToken(ctx context.Context, tokenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, ok := s.tokens[tokenID]
	if !ok {
		return errors.New("令牌未找到")
	}

	// 设置为已撤销
	token.Revoked = true

	// 清除刷新令牌映射
	if token.RefreshToken != "" {
		delete(s.refreshTokens, token.RefreshToken)
	}

	return nil
}

// IsTokenValid 检查令牌是否有效
func (s *InMemoryTokenStore) IsTokenValid(ctx context.Context, tokenID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token, ok := s.tokens[tokenID]
	if !ok {
		return false, nil
	}

	// 检查是否已撤销
	if token.Revoked {
		return false, nil
	}

	// 检查是否已过期
	now := time.Now().Unix()
	if token.ExpiresAt < now {
		return false, nil
	}

	return true, nil
}

// CleanupExpiredTokens 清理过期令牌
func (s *InMemoryTokenStore) CleanupExpiredTokens(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()

	for id, token := range s.tokens {
		// 清理已过期或已撤销的令牌
		if token.ExpiresAt < now || token.Revoked {
			// 清除刷新令牌映射
			if token.RefreshToken != "" {
				delete(s.refreshTokens, token.RefreshToken)
			}

			// 清除令牌
			delete(s.tokens, id)
		}
	}

	return nil
}

// GetUserByID 获取用户信息
func (s *InMemoryTokenStore) GetUserByID(ctx context.Context, userID string) (*auth.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[userID]
	if !ok {
		return nil, auth.ErrUserNotFound
	}

	return user, nil
}

// StoreUser 存储用户信息
func (s *InMemoryTokenStore) StoreUser(user *auth.User) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[user.ID] = user
}

// Close 关闭存储
func (s *InMemoryTokenStore) Close() error {
	// 内存存储无需特殊关闭操作
	return nil
}
