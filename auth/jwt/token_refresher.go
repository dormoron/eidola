package jwt

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth"
)

// RefreshCallback 令牌刷新回调函数
type RefreshCallback func(oldToken, newToken string)

// RefresherOption 令牌刷新器选项
type RefresherOption func(*TokenRefresher)

// WithRefreshThreshold 设置刷新阈值
func WithRefreshThreshold(threshold time.Duration) RefresherOption {
	return func(r *TokenRefresher) {
		r.refreshThreshold = threshold
	}
}

// WithCheckInterval 设置检查间隔
func WithCheckInterval(interval time.Duration) RefresherOption {
	return func(r *TokenRefresher) {
		r.checkInterval = interval
	}
}

// TokenRefresher 令牌刷新器
type TokenRefresher struct {
	// 令牌管理器
	tokenManager *JWTManager

	// 刷新阈值（在令牌过期前多长时间刷新）
	refreshThreshold time.Duration

	// 检查间隔
	checkInterval time.Duration

	// 注册的令牌映射
	tokens map[string]*tokenInfo

	// 保护共享资源的锁
	mu sync.RWMutex

	// 检查定时器
	checkTimer *time.Timer

	// 通知停止检查goroutine
	stopCh chan struct{}

	// 是否已启动
	started bool
}

// tokenInfo 令牌信息
type tokenInfo struct {
	// 令牌字符串
	tokenString string

	// 回调通道
	callbackCh chan RefreshCallback

	// 过期时间
	expiry time.Time

	// 已刷新标志
	refreshed bool
}

// NewTokenRefresher 创建新的令牌刷新器
func NewTokenRefresher(tokenManager *JWTManager, opts ...RefresherOption) *TokenRefresher {
	refresher := &TokenRefresher{
		tokenManager:     tokenManager,
		refreshThreshold: time.Minute * 5, // 默认5分钟
		checkInterval:    time.Minute,     // 默认1分钟
		tokens:           make(map[string]*tokenInfo),
		stopCh:           make(chan struct{}),
	}

	// 应用选项
	for _, opt := range opts {
		opt(refresher)
	}

	return refresher
}

// Start 启动令牌刷新器
func (r *TokenRefresher) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return errors.New("token refresher already started")
	}

	// 启动检查goroutine
	go r.checkLoop(ctx)

	r.started = true
	return nil
}

// Stop 停止令牌刷新器
func (r *TokenRefresher) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	// 停止检查goroutine
	close(r.stopCh)
	if r.checkTimer != nil {
		r.checkTimer.Stop()
	}

	r.started = false
	return nil
}

// RegisterToken 注册令牌以进行自动刷新
func (r *TokenRefresher) RegisterToken(token string, callback RefreshCallback) (chan RefreshCallback, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 验证令牌，获取过期时间
	claims, err := r.tokenManager.ValidateToken(token)
	if err != nil {
		return nil, err
	}

	// 获取过期时间
	var expTime time.Time
	exp, ok := claims["exp"]
	if !ok {
		return nil, errors.New("missing expiry claim in token")
	}

	// 根据类型解析exp
	switch v := exp.(type) {
	case float64:
		// JWT库可能将exp解析为float64
		expTime = time.Unix(int64(v), 0)
	case time.Time:
		expTime = v
	default:
		return nil, errors.New("invalid token expiry format")
	}

	// 创建回调通道
	callbackCh := make(chan RefreshCallback, 1)
	callbackCh <- callback

	// 存储令牌信息
	r.tokens[token] = &tokenInfo{
		tokenString: token,
		callbackCh:  callbackCh,
		expiry:      expTime,
		refreshed:   false,
	}

	return callbackCh, nil
}

// UnregisterToken 取消注册令牌
func (r *TokenRefresher) UnregisterToken(callbackCh chan RefreshCallback) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 查找并移除令牌
	for token, info := range r.tokens {
		if info.callbackCh == callbackCh {
			delete(r.tokens, token)
			close(callbackCh)
			return
		}
	}
}

// RefreshToken 立即刷新令牌
func (r *TokenRefresher) RefreshToken(ctx context.Context, token string) (string, error) {
	// 验证令牌，获取过期时间
	_, err := r.tokenManager.ValidateToken(token)
	if err != nil && err != auth.ErrTokenExpired {
		return "", err
	}

	// 刷新令牌
	newToken, err := r.tokenManager.RefreshToken(token)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 查找令牌信息
	info, exists := r.tokens[token]
	if exists {
		// 更新令牌信息
		delete(r.tokens, token)

		// 验证新令牌，获取过期时间
		newClaims, err := r.tokenManager.ValidateToken(newToken)
		if err != nil {
			return "", err
		}

		// 获取新过期时间
		var newExpTime time.Time
		newExp, ok := newClaims["exp"]
		if !ok {
			return "", errors.New("missing expiry claim in new token")
		}

		// 根据类型解析exp
		switch v := newExp.(type) {
		case float64:
			// JWT库可能将exp解析为float64
			newExpTime = time.Unix(int64(v), 0)
		case time.Time:
			newExpTime = v
		default:
			return "", errors.New("invalid token expiry format")
		}

		// 存储新令牌信息
		r.tokens[newToken] = &tokenInfo{
			tokenString: newToken,
			callbackCh:  info.callbackCh,
			expiry:      newExpTime,
			refreshed:   true,
		}

		// 调用回调函数
		select {
		case callback := <-info.callbackCh:
			callback(token, newToken)
			info.callbackCh <- callback
		default:
			// 没有回调函数
		}
	}

	return newToken, nil
}

// CheckAndRefreshToken 检查令牌是否需要刷新，并在需要时刷新
func (r *TokenRefresher) CheckAndRefreshToken(ctx context.Context, token string, now time.Time) (string, bool, error) {
	r.mu.RLock()
	info, exists := r.tokens[token]
	r.mu.RUnlock()

	if !exists {
		return token, false, nil
	}

	// 计算剩余时间
	remaining := info.expiry.Sub(now)

	// 如果剩余时间小于刷新阈值，刷新令牌
	if remaining > 0 && remaining <= r.refreshThreshold {
		newToken, err := r.RefreshToken(ctx, token)
		if err != nil {
			return token, false, err
		}

		return newToken, true, nil
	}

	return token, false, nil
}

// checkLoop 定期检查所有令牌
func (r *TokenRefresher) checkLoop(ctx context.Context) {
	r.checkTimer = time.NewTimer(r.checkInterval)
	defer r.checkTimer.Stop()

	for {
		select {
		case <-r.checkTimer.C:
			r.checkAndRefreshTokens(ctx)
			r.checkTimer.Reset(r.checkInterval)
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// checkAndRefreshTokens 检查并刷新所有令牌
func (r *TokenRefresher) checkAndRefreshTokens(ctx context.Context) {
	now := time.Now()

	r.mu.RLock()
	// 复制令牌列表，以避免在迭代过程中修改map
	tokenList := make([]string, 0, len(r.tokens))
	for token := range r.tokens {
		tokenList = append(tokenList, token)
	}
	r.mu.RUnlock()

	// 检查每个令牌
	for _, token := range tokenList {
		_, _, _ = r.CheckAndRefreshToken(ctx, token, now)
	}
}

// AutoRefreshClient 自动刷新客户端
type AutoRefreshClient struct {
	// 当前令牌
	currentToken string
	// 令牌刷新器
	refresher *TokenRefresher
	// 回调通道
	callbackCh chan RefreshCallback
	// 保护共享资源的锁
	mu sync.RWMutex
}

// NewAutoRefreshClient 创建新的自动刷新客户端
func NewAutoRefreshClient(initialToken string, refresher *TokenRefresher) *AutoRefreshClient {
	return &AutoRefreshClient{
		currentToken: initialToken,
		refresher:    refresher,
	}
}

// RegisterToken 注册令牌以进行自动刷新
func (c *AutoRefreshClient) RegisterToken(callback RefreshCallback) (chan RefreshCallback, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 注册令牌
	callbackCh, err := c.refresher.RegisterToken(c.currentToken, func(oldToken, newToken string) {
		// 更新当前令牌
		c.mu.Lock()
		c.currentToken = newToken
		c.mu.Unlock()

		// 调用用户回调
		callback(oldToken, newToken)
	})

	if err != nil {
		return nil, err
	}

	c.callbackCh = callbackCh
	return callbackCh, nil
}

// UnregisterToken 取消注册令牌
func (c *AutoRefreshClient) UnregisterToken(callbackCh chan RefreshCallback) {
	c.refresher.UnregisterToken(callbackCh)
}

// GetCurrentToken 获取当前令牌
func (c *AutoRefreshClient) GetCurrentToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentToken
}

// ForceRefresh 强制刷新令牌
func (c *AutoRefreshClient) ForceRefresh(ctx context.Context) error {
	c.mu.RLock()
	token := c.currentToken
	c.mu.RUnlock()

	// 刷新令牌
	_, err := c.refresher.RefreshToken(ctx, token)
	return err
}
