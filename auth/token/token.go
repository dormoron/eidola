package token

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth"
)

// TokenClaims 定义令牌的声明
type TokenClaims struct {
	// 用户ID
	UserID string `json:"uid"`
	// 用户名
	Username string `json:"uname"`
	// 角色列表
	Roles []string `json:"roles,omitempty"`
	// 权限列表
	Permissions []string `json:"perms,omitempty"`
	// 元数据
	Metadata map[string]string `json:"meta,omitempty"`
	// 令牌ID
	TokenID string `json:"jti"`
	// 颁发时间
	IssuedAt int64 `json:"iat"`
	// 过期时间
	ExpiresAt int64 `json:"exp"`
	// 颁发者
	Issuer string `json:"iss,omitempty"`
}

// CustomTokenManager 实现了自定义的令牌管理
type CustomTokenManager struct {
	// 使用密钥进行签名
	signingKey []byte
	// 令牌有效期
	defaultDuration time.Duration
	// 颁发者名称
	issuer string
	// 加密器
	encryptor Encryptor
	// 令牌存储
	tokenStore TokenStore
	// 令牌黑名单
	blacklist TokenBlacklist
	// 互斥锁
	mu sync.RWMutex
}

// CustomTokenConfig 配置自定义令牌管理器
type CustomTokenConfig struct {
	// 签名密钥
	SigningKey []byte
	// 默认有效期
	DefaultDuration time.Duration
	// 颁发者
	Issuer string
	// 加密器
	Encryptor Encryptor
	// 令牌存储
	TokenStore TokenStore
	// 令牌黑名单
	Blacklist TokenBlacklist
}

// NewCustomTokenManager 创建自定义令牌管理器
func NewCustomTokenManager(config CustomTokenConfig) (*CustomTokenManager, error) {
	if len(config.SigningKey) == 0 {
		return nil, errors.New("签名密钥不能为空")
	}

	if config.DefaultDuration <= 0 {
		config.DefaultDuration = time.Hour
	}

	if config.TokenStore == nil {
		// 默认使用内存存储
		config.TokenStore = NewInMemoryTokenStore()
	}

	if config.Encryptor == nil {
		// 默认使用HMAC加密
		encryptor, err := NewHmacEncryptor(config.SigningKey)
		if err != nil {
			return nil, err
		}
		config.Encryptor = encryptor
	}

	// 如果未提供黑名单，创建内存黑名单
	if config.Blacklist == nil {
		config.Blacklist = NewInMemoryBlacklist()
	}

	return &CustomTokenManager{
		signingKey:      config.SigningKey,
		defaultDuration: config.DefaultDuration,
		issuer:          config.Issuer,
		encryptor:       config.Encryptor,
		tokenStore:      config.TokenStore,
		blacklist:       config.Blacklist,
	}, nil
}

// GenerateToken 生成自定义令牌
func (m *CustomTokenManager) GenerateToken(ctx context.Context, user *auth.User, duration time.Duration) (*auth.TokenInfo, error) {
	if duration <= 0 {
		duration = m.defaultDuration
	}

	// 生成随机令牌ID
	tokenID, err := generateRandomTokenID(32)
	if err != nil {
		return nil, fmt.Errorf("生成令牌ID失败: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(duration)

	// 创建令牌声明
	claims := &TokenClaims{
		UserID:      user.ID,
		Username:    user.Username,
		Roles:       user.Roles,
		Permissions: user.Permissions,
		Metadata:    user.Metadata,
		TokenID:     tokenID,
		IssuedAt:    now.Unix(),
		ExpiresAt:   expiresAt.Unix(),
		Issuer:      m.issuer,
	}

	// 序列化声明
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("序列化令牌声明失败: %w", err)
	}

	// 对令牌进行签名
	signature, err := m.encryptor.Encrypt(payload)
	if err != nil {
		return nil, fmt.Errorf("令牌签名失败: %w", err)
	}

	// 编码有效载荷
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	encodedSignature := base64.RawURLEncoding.EncodeToString(signature)

	// 将有效载荷和签名组合成令牌
	token := fmt.Sprintf("%s.%s", encodedPayload, encodedSignature)

	// 生成刷新令牌
	refreshTokenID, err := generateRandomTokenID(48)
	if err != nil {
		return nil, fmt.Errorf("生成刷新令牌ID失败: %w", err)
	}

	// 将令牌存储到令牌存储中
	err = m.tokenStore.StoreToken(ctx, &TokenData{
		AccessToken:  token,
		RefreshToken: refreshTokenID,
		UserID:       user.ID,
		ExpiresAt:    expiresAt.Unix(),
		TokenID:      tokenID,
	})
	if err != nil {
		return nil, fmt.Errorf("存储令牌失败: %w", err)
	}

	return &auth.TokenInfo{
		AccessToken:  token,
		RefreshToken: refreshTokenID,
		ExpiresAt:    expiresAt,
		TokenType:    "Bearer",
	}, nil
}

// ValidateToken 验证令牌
func (m *CustomTokenManager) ValidateToken(ctx context.Context, tokenString string) (*auth.User, error) {
	// 检查令牌是否为空
	if tokenString == "" {
		return nil, auth.ErrInvalidToken
	}

	// 分割令牌
	parts := strings.Split(tokenString, ".")
	if len(parts) != 2 {
		return nil, auth.ErrInvalidToken
	}

	// 解码有效载荷和签名
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, auth.ErrInvalidToken
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, auth.ErrInvalidToken
	}

	// 验证签名
	if err := m.encryptor.Verify(payload, signature); err != nil {
		return nil, auth.ErrInvalidToken
	}

	// 解析声明
	claims := &TokenClaims{}
	if err := json.Unmarshal(payload, claims); err != nil {
		return nil, auth.ErrInvalidToken
	}

	// 检查令牌是否过期
	now := time.Now().Unix()
	if claims.ExpiresAt < now {
		return nil, auth.ErrTokenExpired
	}

	// 检查令牌是否在黑名单中
	if m.blacklist != nil {
		isBlacklisted, err := m.blacklist.IsBlacklisted(ctx, claims.TokenID)
		if err != nil {
			return nil, fmt.Errorf("检查黑名单失败: %w", err)
		}
		if isBlacklisted {
			return nil, auth.ErrTokenRevoked
		}
	}

	// 检查令牌是否被撤销
	if m.tokenStore != nil {
		valid, err := m.tokenStore.IsTokenValid(ctx, claims.TokenID)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, auth.ErrTokenRevoked
		}
	}

	// 构建用户
	user := &auth.User{
		ID:          claims.UserID,
		Username:    claims.Username,
		Roles:       claims.Roles,
		Permissions: claims.Permissions,
		Metadata:    claims.Metadata,
	}

	return user, nil
}

// RefreshToken 刷新令牌
func (m *CustomTokenManager) RefreshToken(ctx context.Context, refreshToken string) (*auth.TokenInfo, error) {
	if refreshToken == "" {
		return nil, auth.ErrInvalidToken
	}

	// 从存储中获取令牌数据
	tokenData, err := m.tokenStore.GetTokenByRefresh(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("获取刷新令牌失败: %w", err)
	}

	// 检查令牌是否过期
	now := time.Now().Unix()
	if tokenData.ExpiresAt < now {
		return nil, auth.ErrTokenExpired
	}

	// 检查令牌是否在黑名单中
	if m.blacklist != nil {
		isBlacklisted, err := m.blacklist.IsBlacklisted(ctx, tokenData.TokenID)
		if err != nil {
			return nil, fmt.Errorf("检查黑名单失败: %w", err)
		}
		if isBlacklisted {
			return nil, auth.ErrTokenRevoked
		}
	}

	// 撤销旧令牌
	err = m.tokenStore.RevokeToken(ctx, tokenData.TokenID)
	if err != nil {
		return nil, fmt.Errorf("撤销旧令牌失败: %w", err)
	}

	// 将旧令牌添加到黑名单
	if m.blacklist != nil {
		timeUntilExpiry := time.Duration(tokenData.ExpiresAt-now) * time.Second
		err = m.blacklist.AddToBlacklist(ctx, tokenData.TokenID, timeUntilExpiry)
		if err != nil {
			return nil, fmt.Errorf("添加令牌到黑名单失败: %w", err)
		}
	}

	// 构建用户信息
	user, err := m.tokenStore.GetUserByID(ctx, tokenData.UserID)
	if err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 生成新令牌
	return m.GenerateToken(ctx, user, m.defaultDuration)
}

// RevokeToken 撤销令牌
func (m *CustomTokenManager) RevokeToken(ctx context.Context, tokenString string) error {
	// 分割令牌
	parts := strings.Split(tokenString, ".")
	if len(parts) != 2 {
		return auth.ErrInvalidToken
	}

	// 解码有效载荷
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return auth.ErrInvalidToken
	}

	// 解析声明
	claims := &TokenClaims{}
	if err := json.Unmarshal(payload, claims); err != nil {
		return auth.ErrInvalidToken
	}

	// 撤销令牌
	if m.tokenStore != nil {
		err := m.tokenStore.RevokeToken(ctx, claims.TokenID)
		if err != nil {
			return err
		}
	}

	// 将令牌添加到黑名单
	if m.blacklist != nil {
		now := time.Now().Unix()
		timeUntilExpiry := time.Duration(claims.ExpiresAt-now) * time.Second
		// 如果令牌已过期，使用短期限制防止重放攻击
		if timeUntilExpiry <= 0 {
			timeUntilExpiry = time.Hour * 24 // 1天
		}

		err := m.blacklist.AddToBlacklist(ctx, claims.TokenID, timeUntilExpiry)
		if err != nil {
			return fmt.Errorf("添加令牌到黑名单失败: %w", err)
		}
	}

	return nil
}

// CleanupExpired 清理过期的令牌和黑名单条目
func (m *CustomTokenManager) CleanupExpired(ctx context.Context) error {
	// 清理过期令牌
	if m.tokenStore != nil {
		if err := m.tokenStore.CleanupExpiredTokens(ctx); err != nil {
			return fmt.Errorf("清理过期令牌失败: %w", err)
		}
	}

	// 清理过期黑名单条目
	if m.blacklist != nil {
		if err := m.blacklist.CleanupExpired(ctx); err != nil {
			return fmt.Errorf("清理过期黑名单条目失败: %w", err)
		}
	}

	return nil
}

// Close 关闭令牌管理器
func (m *CustomTokenManager) Close() error {
	var errs []error

	// 关闭令牌存储
	if m.tokenStore != nil {
		if err := m.tokenStore.Close(); err != nil {
			errs = append(errs, fmt.Errorf("关闭令牌存储失败: %w", err))
		}
	}

	// 关闭黑名单
	if m.blacklist != nil {
		if err := m.blacklist.Close(); err != nil {
			errs = append(errs, fmt.Errorf("关闭黑名单失败: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("关闭令牌管理器时发生错误: %v", errs)
	}

	return nil
}

// generateRandomTokenID 生成随机令牌ID
func generateRandomTokenID(length int) (string, error) {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
