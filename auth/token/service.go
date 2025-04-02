package token

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/dormoron/eidola/auth"
)

// AuthenticatorFunc 定义验证用户凭证的函数类型
type AuthenticatorFunc func(ctx context.Context, username, password string) (*auth.User, error)

// TokenService 提供完整的令牌服务
type TokenService struct {
	// 令牌管理器
	tokenManager auth.TokenManager
	// 认证器
	authenticator AuthenticatorFunc
	// 授权器
	authorizer auth.Authorizer
	// 令牌提取器
	extractor auth.TokenExtractor
	// 安全选项
	options TokenServiceOptions
	// 互斥锁
	mu sync.RWMutex
}

// TokenServiceOptions 令牌服务选项
type TokenServiceOptions struct {
	// 访问令牌有效期
	AccessTokenDuration time.Duration
	// 刷新令牌有效期
	RefreshTokenDuration time.Duration
	// 是否启用刷新令牌轮换
	EnableRefreshTokenRotation bool
	// 是否启用令牌撤销
	EnableTokenRevocation bool
	// 令牌类型
	TokenType string
}

// NewTokenService 创建令牌服务
func NewTokenService(
	tokenManager auth.TokenManager,
	authenticator AuthenticatorFunc,
	extractor auth.TokenExtractor,
	options TokenServiceOptions,
) *TokenService {
	if options.AccessTokenDuration <= 0 {
		options.AccessTokenDuration = time.Hour
	}

	if options.RefreshTokenDuration <= 0 {
		options.RefreshTokenDuration = time.Hour * 24 * 7
	}

	if options.TokenType == "" {
		options.TokenType = "Bearer"
	}

	return &TokenService{
		tokenManager:  tokenManager,
		authenticator: authenticator,
		extractor:     extractor,
		options:       options,
	}
}

// SetAuthorizer 设置授权器
func (s *TokenService) SetAuthorizer(authorizer auth.Authorizer) {
	s.authorizer = authorizer
}

// Login 用户登录并生成令牌
func (s *TokenService) Login(ctx context.Context, username, password string) (*auth.TokenInfo, error) {
	// 验证用户凭证
	user, err := s.authenticator(ctx, username, password)
	if err != nil {
		return nil, err
	}

	// 生成令牌
	tokenInfo, err := s.tokenManager.GenerateToken(ctx, user, s.options.AccessTokenDuration)
	if err != nil {
		return nil, err
	}

	return tokenInfo, nil
}

// Refresh 刷新令牌
func (s *TokenService) Refresh(ctx context.Context, refreshToken string) (*auth.TokenInfo, error) {
	if refreshToken == "" {
		return nil, auth.ErrInvalidToken
	}

	// 刷新令牌
	tokenInfo, err := s.tokenManager.RefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	return tokenInfo, nil
}

// Validate 验证令牌
func (s *TokenService) Validate(ctx context.Context) (*auth.User, error) {
	// 从上下文中提取令牌
	token, err := s.extractor.Extract(ctx)
	if err != nil {
		return nil, err
	}

	// 验证令牌
	user, err := s.tokenManager.ValidateToken(ctx, token)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// Logout 用户登出并撤销令牌
func (s *TokenService) Logout(ctx context.Context) error {
	if !s.options.EnableTokenRevocation {
		return nil
	}

	// 从上下文中提取令牌
	token, err := s.extractor.Extract(ctx)
	if err != nil {
		return err
	}

	// 撤销令牌
	return s.tokenManager.RevokeToken(ctx, token)
}

// CheckPermission 检查用户权限
func (s *TokenService) CheckPermission(ctx context.Context, resource, action string) error {
	if s.authorizer == nil {
		return errors.New("授权器未设置")
	}

	// 验证令牌并获取用户
	user, err := s.Validate(ctx)
	if err != nil {
		return err
	}

	// 检查权限
	allowed, err := s.authorizer.CheckPermission(ctx, user, resource, action)
	if err != nil {
		return err
	}

	if !allowed {
		return auth.ErrPermissionDenied
	}

	return nil
}

// GenerateAPIKey 生成API密钥
func (s *TokenService) GenerateAPIKey(ctx context.Context, userID string, duration time.Duration) (string, error) {
	// 创建API密钥用户
	user := &auth.User{
		ID:       userID,
		Username: "api-key",
		Roles:    []string{"api"},
	}

	// 如果没有指定有效期，使用默认值
	if duration <= 0 {
		duration = s.options.AccessTokenDuration
	}

	// 生成随机密钥
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		return "", err
	}

	apiKey := base64.RawURLEncoding.EncodeToString(key)

	// 将API密钥存储为令牌
	_, err = s.tokenManager.GenerateToken(ctx, user, duration)
	if err != nil {
		return "", err
	}

	return apiKey, nil
}

// 定义鉴权中间件接口
type Middleware interface {
	// Authenticate 认证中间件
	Authenticate(next interface{}) interface{}
	// Authorize 授权中间件
	Authorize(resource, action string, next interface{}) interface{}
}

// GRPC服务端示例使用方法：
/*
import (
	"github.com/dormoron/eidola/auth"
	"github.com/dormoron/eidola/auth/token"
)

func SetupTokenService() *token.TokenService {
	// 创建令牌存储
	tokenStore := token.NewInMemoryTokenStore()

	// 创建密钥
	signingKey := []byte("your-secret-key")

	// 创建加密器
	encryptor, _ := token.NewHmacEncryptor(signingKey)

	// 创建令牌管理器
	tokenManager, _ := token.NewCustomTokenManager(token.CustomTokenConfig{
		SigningKey:     signingKey,
		DefaultDuration: time.Hour,
		Issuer:         "eidola-service",
		Encryptor:      encryptor,
		TokenStore:     tokenStore,
	})

	// 创建用户认证函数
	authenticator := func(ctx context.Context, username, password string) (*auth.User, error) {
		// 在实际应用中，这里应该查询数据库验证用户凭证
		if username == "admin" && password == "password" {
			return &auth.User{
				ID:       "1",
				Username: username,
				Roles:    []string{"admin"},
				Permissions: []string{"*"},
			}, nil
		}
		return nil, auth.ErrInvalidCredentials
	}

	// 创建令牌提取器
	extractor := auth.NewMetadataTokenExtractor("authorization", "Bearer ")

	// 创建令牌服务
	tokenService := token.NewTokenService(
		tokenManager,
		authenticator,
		extractor,
		token.TokenServiceOptions{
			AccessTokenDuration:       time.Hour,
			RefreshTokenDuration:      time.Hour * 24 * 7,
			EnableRefreshTokenRotation: true,
			EnableTokenRevocation:     true,
		},
	)

	// 创建授权器
	authorizer := auth.NewRBACAuthorizer()
	authorizer.AddRole("admin", []string{"*"})
	authorizer.AddRole("user", []string{"user:read"})

	// 设置授权器
	tokenService.SetAuthorizer(authorizer)

	return tokenService
}

// 使用认证拦截器
func SetupGRPCServer(tokenService *token.TokenService) *grpc.Server {
	// 创建令牌提取器
	extractor := auth.NewMetadataTokenExtractor("authorization", "Bearer ")

	// 创建授权器
	authorizer := auth.NewRBACAuthorizer()

	// 创建认证拦截器
	interceptor := auth.NewAuthInterceptor(extractor, tokenService.TokenManager(), authorizer)

	// 添加公开方法
	interceptor.AddPublicMethod("/service.AuthService/Login")
	interceptor.AddPublicMethod("/service.AuthService/Refresh")

	// 添加资源映射
	interceptor.AddResourceMapping("/service.UserService/GetUser", "user", "read")
	interceptor.AddResourceMapping("/service.UserService/UpdateUser", "user", "write")

	// 创建GRPC服务器
	server := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor.UnaryServerInterceptor()),
		grpc.StreamInterceptor(interceptor.StreamServerInterceptor()),
	)

	return server
}
*/

// 添加一个GetTokenManager方法，让外部代码可以访问tokenManager
func (s *TokenService) GetTokenManager() auth.TokenManager {
	return s.tokenManager
}
