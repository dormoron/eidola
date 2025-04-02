package auth

import (
	"context"
	"errors"
	"time"
)

// 定义常见错误
var (
	ErrInvalidCredentials     = errors.New("无效的凭证")
	ErrInvalidToken           = errors.New("无效的令牌")
	ErrTokenExpired           = errors.New("令牌已过期")
	ErrPermissionDenied       = errors.New("权限被拒绝")
	ErrUserNotFound           = errors.New("用户未找到")
	ErrAuthServiceUnavailable = errors.New("认证服务不可用")
	ErrTokenRevoked           = errors.New("令牌已被撤销")
)

// Credential 表示用户凭证
type Credential struct {
	// 用户名或用户标识符
	Username string
	// 密码或令牌
	Password string
	// 其他认证信息
	Extra map[string]string
}

// User 表示认证后的用户信息
type User struct {
	// 用户标识符
	ID string
	// 用户名
	Username string
	// 角色列表
	Roles []string
	// 权限列表
	Permissions []string
	// 用户扩展信息
	Metadata map[string]string
}

// TokenInfo 表示令牌信息
type TokenInfo struct {
	// 访问令牌 (用于验证用户身份)
	AccessToken string
	// 刷新令牌 (用于获取新的访问令牌)
	RefreshToken string
	// 访问令牌有效期
	ExpiresAt time.Time
	// 令牌类型
	TokenType string
	// 令牌作用域
	Scope string
}

// Authenticator 定义认证接口
type Authenticator interface {
	// Authenticate 验证用户凭证并返回用户信息
	Authenticate(ctx context.Context, credential Credential) (*User, error)
}

// TokenManager 定义令牌管理接口
type TokenManager interface {
	// GenerateToken 生成令牌
	GenerateToken(ctx context.Context, user *User, duration time.Duration) (*TokenInfo, error)
	// ValidateToken 验证令牌
	ValidateToken(ctx context.Context, token string) (*User, error)
	// RefreshToken 刷新令牌
	RefreshToken(ctx context.Context, refreshToken string) (*TokenInfo, error)
	// RevokeToken 撤销令牌
	RevokeToken(ctx context.Context, token string) error
}

// TokenExtractor 定义令牌提取接口
type TokenExtractor interface {
	// Extract 从上下文中提取令牌
	Extract(ctx context.Context) (string, error)
}

// Authorizer 定义授权接口
type Authorizer interface {
	// CheckPermission 检查用户是否有特定权限
	CheckPermission(ctx context.Context, user *User, resource string, action string) (bool, error)
}
