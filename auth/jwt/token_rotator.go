package jwt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth"
	jwt "github.com/golang-jwt/jwt/v5"
)

// KeyRotationStrategy 定义密钥轮换策略
type KeyRotationStrategy int

const (
	// RotateFixed 固定时间间隔轮换
	RotateFixed KeyRotationStrategy = iota
	// RotateOnDemand 按需轮换
	RotateOnDemand
	// RotateHybrid 混合策略（定时+按需）
	RotateHybrid
)

// KeyStorage 密钥存储接口
type KeyStorage interface {
	// StoreKey 存储密钥
	StoreKey(ctx context.Context, keyID string, key []byte, validFrom, validUntil time.Time) error

	// GetKey 获取密钥
	GetKey(ctx context.Context, keyID string) ([]byte, error)

	// GetCurrentKey 获取当前有效的密钥
	GetCurrentKey(ctx context.Context) (string, []byte, error)

	// GetKeyByTime 获取指定时间有效的密钥
	GetKeyByTime(ctx context.Context, t time.Time) (string, []byte, error)

	// ListKeys 列出所有密钥
	ListKeys(ctx context.Context) (map[string]time.Time, error)

	// DeleteKey 删除密钥
	DeleteKey(ctx context.Context, keyID string) error
}

// MemoryKeyStorage 内存密钥存储
type MemoryKeyStorage struct {
	// 密钥映射，键为ID，值为密钥信息
	keys map[string]*keyInfo
	// 保护共享资源的锁
	mu sync.RWMutex
}

// keyInfo 密钥信息
type keyInfo struct {
	// 密钥数据
	key []byte
	// 生效时间
	validFrom time.Time
	// 过期时间
	validUntil time.Time
}

// NewMemoryKeyStorage 创建新的内存密钥存储
func NewMemoryKeyStorage() *MemoryKeyStorage {
	return &MemoryKeyStorage{
		keys: make(map[string]*keyInfo),
	}
}

// StoreKey 存储密钥
func (s *MemoryKeyStorage) StoreKey(ctx context.Context, keyID string, key []byte, validFrom, validUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.keys[keyID] = &keyInfo{
		key:        key,
		validFrom:  validFrom,
		validUntil: validUntil,
	}

	return nil
}

// GetKey 获取密钥
func (s *MemoryKeyStorage) GetKey(ctx context.Context, keyID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, exists := s.keys[keyID]
	if !exists {
		return nil, fmt.Errorf("key %s not found", keyID)
	}

	return info.key, nil
}

// GetCurrentKey 获取当前有效的密钥
func (s *MemoryKeyStorage) GetCurrentKey(ctx context.Context) (string, []byte, error) {
	return s.GetKeyByTime(ctx, time.Now())
}

// GetKeyByTime 获取指定时间有效的密钥
func (s *MemoryKeyStorage) GetKeyByTime(ctx context.Context, t time.Time) (string, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		latestKeyID  string
		latestKey    []byte
		latestKeyGen time.Time
	)

	// 遍历所有密钥，找到指定时间有效的最新密钥
	for keyID, info := range s.keys {
		if (info.validFrom.Before(t) || info.validFrom.Equal(t)) &&
			(info.validUntil.After(t) || info.validUntil.Equal(t)) &&
			(latestKeyGen.IsZero() || info.validFrom.After(latestKeyGen)) {
			latestKeyID = keyID
			latestKey = info.key
			latestKeyGen = info.validFrom
		}
	}

	if latestKeyID == "" {
		return "", nil, errors.New("no valid key found")
	}

	return latestKeyID, latestKey, nil
}

// ListKeys 列出所有密钥
func (s *MemoryKeyStorage) ListKeys(ctx context.Context) (map[string]time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]time.Time)
	for keyID, info := range s.keys {
		result[keyID] = info.validFrom
	}

	return result, nil
}

// DeleteKey 删除密钥
func (s *MemoryKeyStorage) DeleteKey(ctx context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.keys[keyID]; !exists {
		return fmt.Errorf("key %s not found", keyID)
	}

	delete(s.keys, keyID)
	return nil
}

// TokenRotator 令牌轮换器
type TokenRotator struct {
	// 密钥存储
	storage KeyStorage
	// 密钥长度（字节）
	keyLength int
	// 轮换策略
	strategy KeyRotationStrategy
	// 轮换间隔
	rotationInterval time.Duration
	// 密钥重叠期
	keyOverlapPeriod time.Duration
	// 自动轮换定时器
	rotationTimer *time.Timer
	// 保护共享资源的锁
	mu sync.Mutex
	// 是否已启动
	started bool
	// 通知停止轮换goroutine
	stopCh chan struct{}
}

// NewTokenRotator 创建新的令牌轮换器
func NewTokenRotator(
	storage KeyStorage,
	keyLength int,
	strategy KeyRotationStrategy,
	rotationInterval time.Duration,
	keyOverlapPeriod time.Duration,
) *TokenRotator {
	if keyLength <= 0 {
		keyLength = 32 // 默认32字节（256位）
	}

	if rotationInterval <= 0 {
		rotationInterval = 24 * time.Hour // 默认24小时
	}

	if keyOverlapPeriod <= 0 {
		keyOverlapPeriod = 1 * time.Hour // 默认1小时
	}

	return &TokenRotator{
		storage:          storage,
		keyLength:        keyLength,
		strategy:         strategy,
		rotationInterval: rotationInterval,
		keyOverlapPeriod: keyOverlapPeriod,
		stopCh:           make(chan struct{}),
	}
}

// Start 启动令牌轮换器
func (r *TokenRotator) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return errors.New("token rotator already started")
	}

	// 生成初始密钥
	if err := r.generateKey(ctx); err != nil {
		return err
	}

	// 启动轮换goroutine
	if r.strategy == RotateFixed || r.strategy == RotateHybrid {
		go r.rotateLoop(ctx)
	}

	r.started = true
	return nil
}

// Stop 停止令牌轮换器
func (r *TokenRotator) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	// 停止轮换goroutine
	close(r.stopCh)
	if r.rotationTimer != nil {
		r.rotationTimer.Stop()
	}

	r.started = false
	return nil
}

// RotateNow 立即轮换密钥
func (r *TokenRotator) RotateNow(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return errors.New("token rotator not started")
	}

	return r.generateKey(ctx)
}

// GetCurrentKey 获取当前有效的密钥
func (r *TokenRotator) GetCurrentKey(ctx context.Context) (string, []byte, error) {
	return r.storage.GetCurrentKey(ctx)
}

// GetKeyByTime 获取指定时间有效的密钥
func (r *TokenRotator) GetKeyByTime(ctx context.Context, t time.Time) (string, []byte, error) {
	return r.storage.GetKeyByTime(ctx, t)
}

// generateKey 生成新密钥
func (r *TokenRotator) generateKey(ctx context.Context) error {
	// 生成随机密钥
	key := make([]byte, r.keyLength)
	if _, err := rand.Read(key); err != nil {
		return err
	}

	// 生成密钥ID
	keyID := fmt.Sprintf("key-%s", base64.RawURLEncoding.EncodeToString(key[:8]))

	// 计算生效时间和过期时间
	now := time.Now()
	validFrom := now
	validUntil := now.Add(r.rotationInterval + r.keyOverlapPeriod)

	// 存储密钥
	return r.storage.StoreKey(ctx, keyID, key, validFrom, validUntil)
}

// rotateLoop 轮换循环
func (r *TokenRotator) rotateLoop(ctx context.Context) {
	r.rotationTimer = time.NewTimer(r.rotationInterval)
	defer r.rotationTimer.Stop()

	for {
		select {
		case <-r.rotationTimer.C:
			// 生成新密钥
			if err := r.generateKey(ctx); err != nil {
				// 记录错误日志
				fmt.Printf("Error generating key: %v\n", err)
			}

			// 重置定时器
			r.rotationTimer.Reset(r.rotationInterval)

		case <-r.stopCh:
			return
		}
	}
}

// RotatingJWTManager 带密钥轮换的JWT管理器
type RotatingJWTManager struct {
	// 嵌入JWTManager，继承其方法
	manager *JWTManager
	rotator *TokenRotator
	config  JWTConfig
}

// NewRotatingJWTManager 创建带密钥轮换的JWT管理器
func NewRotatingJWTManager(
	rotator *TokenRotator,
	config JWTConfig,
) *RotatingJWTManager {
	return &RotatingJWTManager{
		manager: nil, // 稍后初始化
		rotator: rotator,
		config:  config,
	}
}

// Init 初始化管理器，必须在rotator.Start()之后调用
func (m *RotatingJWTManager) Init(ctx context.Context) error {
	// 获取当前有效的密钥
	_, key, err := m.rotator.GetCurrentKey(ctx)
	if err != nil {
		return err
	}

	// 创建基础JWTManager
	m.manager = NewJWTManager(key, m.config)
	return nil
}

// GenerateToken 生成令牌
func (m *RotatingJWTManager) GenerateToken(user *auth.User) (string, error) {
	if m.manager == nil {
		return "", errors.New("rotating JWT manager not initialized")
	}

	// 获取当前有效的密钥
	keyID, key, err := m.rotator.GetCurrentKey(context.Background())
	if err != nil {
		return "", err
	}

	// 根据当前用户和密钥创建令牌
	now := time.Now()
	expiresAt := now.Add(m.config.Expiry)

	claims := JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    m.config.Issuer,
			Audience:  jwt.ClaimStrings{m.config.Audience},
		},
		UserID:      user.ID,
		Username:    user.Name,
		Roles:       user.Roles,
		Permissions: user.Permissions,
		Metadata:    user.Metadata,
	}

	token := jwt.NewWithClaims(m.config.SigningMethod, claims)
	// 设置密钥ID
	token.Header["kid"] = keyID

	// 签名令牌
	return token.SignedString(key)
}

// ValidateToken 验证令牌
func (m *RotatingJWTManager) ValidateToken(tokenString string) (auth.Claims, error) {
	// 解析令牌以获取密钥ID
	parsed, err := m.parseUnverifiedToken(tokenString)
	if err != nil {
		return nil, fmt.Errorf("invalid token format: %w", err)
	}

	// 获取密钥ID
	var keyID string
	if kid, ok := parsed.Header["kid"].(string); ok {
		keyID = kid
	} else {
		// 如果令牌没有密钥ID，尝试根据令牌签发时间获取密钥
		if claims, ok := parsed.Claims.(jwt.MapClaims); ok {
			if iat, ok := claims["iat"].(float64); ok {
				t := time.Unix(int64(iat), 0)
				keyID, _, err = m.rotator.GetKeyByTime(context.Background(), t)
				if err != nil {
					return nil, fmt.Errorf("invalid token: %w", err)
				}
			}
		}
	}

	// 如果仍然无法确定密钥ID，报错
	if keyID == "" {
		return nil, fmt.Errorf("invalid token: missing key ID")
	}

	// 获取密钥
	key, err := m.rotator.storage.GetKey(context.Background(), keyID)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	// 使用找到的密钥验证令牌
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// 验证签名方法
		if token.Method != m.config.SigningMethod {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return key, nil
	})

	if err != nil {
		// 判断不同类型的JWT错误
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return nil, auth.ErrTokenExpired
		case errors.Is(err, jwt.ErrTokenSignatureInvalid):
			return nil, auth.ErrInvalidToken
		case errors.Is(err, jwt.ErrTokenMalformed),
			errors.Is(err, jwt.ErrTokenNotValidYet),
			errors.Is(err, jwt.ErrTokenInvalidClaims):
			return nil, auth.ErrInvalidToken
		default:
			// 使用错误字符串内容来检查
			if strings.Contains(err.Error(), "token is expired") ||
				strings.Contains(err.Error(), "token has expired") {
				return nil, auth.ErrTokenExpired
			}
			if strings.Contains(err.Error(), "signature is invalid") {
				return nil, auth.ErrInvalidToken
			}
			return nil, err
		}
	}

	if !token.Valid {
		return nil, auth.ErrInvalidToken
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, auth.ErrInvalidToken
	}

	// 转换为通用Claims格式
	result := auth.Claims{
		"uid":         claims.UserID,
		"username":    claims.Username,
		"roles":       claims.Roles,
		"permissions": claims.Permissions,
		"exp":         claims.ExpiresAt,
		"iat":         claims.IssuedAt,
		"nbf":         claims.NotBefore,
		"iss":         claims.Issuer,
		"aud":         claims.Audience,
	}

	// 添加元数据
	if claims.Metadata != nil {
		for k, v := range claims.Metadata {
			result[k] = v
		}
	}

	return result, nil
}

// 解析未验证的令牌以获取密钥ID
func (m *RotatingJWTManager) parseUnverifiedToken(tokenString string) (*jwt.Token, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("invalid token format: %w", err)
	}

	return token, nil
}
