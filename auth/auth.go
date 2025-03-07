package auth

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
)

// ClientInterceptor 是客户端拦截器函数，用于在请求前添加认证信息
type ClientInterceptor func(ctx context.Context) context.Context

// NewClientUnaryInterceptor 创建客户端一元拦截器
func NewClientUnaryInterceptor(interceptor ClientInterceptor) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		newCtx := interceptor(ctx)
		return invoker(newCtx, method, req, reply, cc, opts...)
	}
}

// NewClientStreamInterceptor 创建客户端流拦截器
func NewClientStreamInterceptor(interceptor ClientInterceptor) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		newCtx := interceptor(ctx)
		return streamer(newCtx, desc, cc, method, opts...)
	}
}

// AuthProvider 认证提供器接口
type AuthProvider interface {
	// GetAuthenticator 获取认证器
	GetAuthenticator() Authenticator

	// GetAuthorizer 获取授权器
	GetAuthorizer() Authorizer

	// GetTokenManager 获取令牌管理器
	GetTokenManager() TokenManager

	// GetServerInterceptor 获取服务器拦截器
	GetServerInterceptor() *AuthInterceptor
}

// MultiAuthenticator 多认证器，支持多种认证方式
type MultiAuthenticator struct {
	authenticators []Authenticator
}

// NewMultiAuthenticator 创建新的多认证器
func NewMultiAuthenticator(authenticators ...Authenticator) *MultiAuthenticator {
	return &MultiAuthenticator{
		authenticators: authenticators,
	}
}

// Authenticate 尝试所有认证器认证
func (m *MultiAuthenticator) Authenticate(ctx context.Context, creds Credentials) (*User, error) {
	var lastErr error
	for _, authenticator := range m.authenticators {
		user, err := authenticator.Authenticate(ctx, creds)
		if err == nil {
			return user, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no authenticator available")
	}
	return nil, lastErr
}

// MultiAuthorizer 多授权器，支持多种授权策略
type MultiAuthorizer struct {
	authorizers []Authorizer
	strategy    string // "any" or "all"
}

// NewMultiAuthorizer 创建新的多授权器
func NewMultiAuthorizer(strategy string, authorizers ...Authorizer) *MultiAuthorizer {
	if strategy != "any" && strategy != "all" {
		strategy = "any" // 默认任一授权器授权即可
	}
	return &MultiAuthorizer{
		authorizers: authorizers,
		strategy:    strategy,
	}
}

// Authorize 使用多个授权器授权
func (m *MultiAuthorizer) Authorize(ctx context.Context, user *User, resource string, action string) (bool, error) {
	if len(m.authorizers) == 0 {
		return false, fmt.Errorf("no authorizer available")
	}

	if m.strategy == "all" {
		// 全部授权器必须授权
		for _, authorizer := range m.authorizers {
			allowed, err := authorizer.Authorize(ctx, user, resource, action)
			if err != nil {
				return false, err
			}
			if !allowed {
				return false, nil
			}
		}
		return true, nil
	}

	// 任一授权器授权即可
	var lastErr error
	for _, authorizer := range m.authorizers {
		allowed, err := authorizer.Authorize(ctx, user, resource, action)
		if err != nil {
			lastErr = err
			continue
		}
		if allowed {
			return true, nil
		}
	}

	if lastErr != nil {
		return false, lastErr
	}
	return false, nil
}

// AuthProviderFactory 认证提供器工厂
type AuthProviderFactory struct {
	authenticator Authenticator
	authorizer    Authorizer
	tokenManager  TokenManager
	extractors    []TokenExtractor
}

// NewAuthProviderFactory 创建新的认证提供器工厂
func NewAuthProviderFactory() *AuthProviderFactory {
	return &AuthProviderFactory{
		extractors: make([]TokenExtractor, 0),
	}
}

// WithAuthenticator 设置认证器
func (f *AuthProviderFactory) WithAuthenticator(authenticator Authenticator) *AuthProviderFactory {
	f.authenticator = authenticator
	return f
}

// WithAuthorizer 设置授权器
func (f *AuthProviderFactory) WithAuthorizer(authorizer Authorizer) *AuthProviderFactory {
	f.authorizer = authorizer
	return f
}

// WithTokenManager 设置令牌管理器
func (f *AuthProviderFactory) WithTokenManager(tokenManager TokenManager) *AuthProviderFactory {
	f.tokenManager = tokenManager
	return f
}

// AddExtractor 添加令牌提取器
func (f *AuthProviderFactory) AddExtractor(extractor TokenExtractor) *AuthProviderFactory {
	f.extractors = append(f.extractors, extractor)
	return f
}

// Build 构建认证提供器
func (f *AuthProviderFactory) Build() *DefaultAuthProvider {
	interceptor := NewAuthInterceptor(f.authenticator, f.authorizer)
	for _, extractor := range f.extractors {
		interceptor.AddExtractor(extractor)
	}

	return &DefaultAuthProvider{
		authenticator: f.authenticator,
		authorizer:    f.authorizer,
		tokenManager:  f.tokenManager,
		interceptor:   interceptor,
	}
}

// DefaultAuthProvider 默认认证提供器
type DefaultAuthProvider struct {
	authenticator Authenticator
	authorizer    Authorizer
	tokenManager  TokenManager
	interceptor   *AuthInterceptor
}

// GetAuthenticator 获取认证器
func (p *DefaultAuthProvider) GetAuthenticator() Authenticator {
	return p.authenticator
}

// GetAuthorizer 获取授权器
func (p *DefaultAuthProvider) GetAuthorizer() Authorizer {
	return p.authorizer
}

// GetTokenManager 获取令牌管理器
func (p *DefaultAuthProvider) GetTokenManager() TokenManager {
	return p.tokenManager
}

// GetServerInterceptor 获取服务器拦截器
func (p *DefaultAuthProvider) GetServerInterceptor() *AuthInterceptor {
	return p.interceptor
}

// AuthMethod 表示认证方法
type AuthMethod string

const (
	// AuthMethodJWT JWT认证
	AuthMethodJWT AuthMethod = "jwt"

	// AuthMethodBasic 基本认证
	AuthMethodBasic AuthMethod = "basic"

	// AuthMethodAPIKey API密钥认证
	AuthMethodAPIKey AuthMethod = "apikey"

	// AuthMethodOAuth2 OAuth2认证
	AuthMethodOAuth2 AuthMethod = "oauth2"

	// AuthMethodCustom 自定义认证
	AuthMethodCustom AuthMethod = "custom"
)

// AuthorizationPolicy 表示授权策略
type AuthorizationPolicy string

const (
	// AuthorizationPolicyRBAC 基于角色的访问控制
	AuthorizationPolicyRBAC AuthorizationPolicy = "rbac"

	// AuthorizationPolicyABAC 基于属性的访问控制
	AuthorizationPolicyABAC AuthorizationPolicy = "abac"

	// AuthorizationPolicyCasbin Casbin授权
	AuthorizationPolicyCasbin AuthorizationPolicy = "casbin"

	// AuthorizationPolicyCustom 自定义授权
	AuthorizationPolicyCustom AuthorizationPolicy = "custom"
)
