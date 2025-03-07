package auth

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// contextKey 用于存储认证信息在上下文中的键
type contextKey string

const (
	// AuthUserContextKey 用于在上下文中存储认证用户信息
	AuthUserContextKey contextKey = "auth.user"

	// AuthCredentialsContextKey 用于在上下文中存储认证凭证
	AuthCredentialsContextKey contextKey = "auth.credentials"

	// AuthClaimsContextKey 用于在上下文中存储令牌载荷
	AuthClaimsContextKey contextKey = "auth.claims"
)

// User 表示已认证的用户
type User struct {
	// ID 用户唯一标识
	ID string

	// Name 用户名称
	Name string

	// Roles 用户角色列表
	Roles []string

	// Permissions 用户权限列表
	Permissions []string

	// Metadata 额外的用户元数据
	Metadata map[string]interface{}
}

// Claims 表示令牌中的载荷信息
type Claims map[string]interface{}

// Credentials 表示认证凭证
type Credentials interface {
	// GetToken 返回认证令牌
	GetToken() string

	// GetType 返回认证类型
	GetType() string
}

// Authenticator 认证器接口，验证请求中的凭证
type Authenticator interface {
	// Authenticate 验证凭证并返回用户信息
	Authenticate(ctx context.Context, creds Credentials) (*User, error)
}

// Authorizer 授权器接口，检查用户是否有权限执行操作
type Authorizer interface {
	// Authorize 检查用户是否有权限执行特定操作
	Authorize(ctx context.Context, user *User, resource string, action string) (bool, error)
}

// TokenManager 令牌管理器接口，负责创建和验证令牌
type TokenManager interface {
	// CreateToken 为用户创建新令牌
	CreateToken(user *User, expiry time.Duration) (string, error)

	// ValidateToken 验证令牌并返回载荷
	ValidateToken(token string) (Claims, error)

	// RefreshToken 刷新令牌
	RefreshToken(token string) (string, error)
}

// TokenExtractor 令牌提取器接口，从请求中提取令牌
type TokenExtractor interface {
	// ExtractToken 从上下文中提取令牌
	ExtractToken(ctx context.Context) (Credentials, error)
}

// UnauthorizedError 表示未经授权的错误
type UnauthorizedError struct {
	Message string
}

// Error 实现error接口
func (e *UnauthorizedError) Error() string {
	if e.Message == "" {
		return "unauthorized access"
	}
	return e.Message
}

// AuthenticationError 表示认证失败的错误
type AuthenticationError struct {
	Message string
}

// Error 实现error接口
func (e *AuthenticationError) Error() string {
	if e.Message == "" {
		return "authentication failed"
	}
	return e.Message
}

// 创建标准错误
var (
	ErrUnauthorized    = &UnauthorizedError{Message: "unauthorized access"}
	ErrUnauthenticated = &AuthenticationError{Message: "unauthenticated request"}
	ErrTokenExpired    = &AuthenticationError{Message: "token has expired"}
	ErrInvalidToken    = &AuthenticationError{Message: "invalid token"}
)

// ErrorToStatus 将认证错误转换为gRPC状态错误
func ErrorToStatus(err error) error {
	switch err.(type) {
	case *UnauthorizedError:
		return status.Error(codes.PermissionDenied, err.Error())
	case *AuthenticationError:
		return status.Error(codes.Unauthenticated, err.Error())
	default:
		return err
	}
}

// UserFromContext 从上下文中获取用户信息
func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(AuthUserContextKey).(*User)
	return user, ok
}

// WithUser 向上下文中添加用户信息
func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, AuthUserContextKey, user)
}

// ClaimsFromContext 从上下文中获取载荷信息
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(AuthClaimsContextKey).(Claims)
	return claims, ok
}

// WithClaims 向上下文中添加载荷信息
func WithClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, AuthClaimsContextKey, claims)
}

// AuthInterceptor 创建基于认证器和授权器的拦截器
type AuthInterceptor struct {
	extractors    []TokenExtractor    // 多个令牌提取器
	authenticator Authenticator       // 认证器
	authorizer    Authorizer          // 授权器
	methodRules   map[string][]string // 方法级权限规则
}

// NewAuthInterceptor 创建新的认证拦截器
func NewAuthInterceptor(authenticator Authenticator, authorizer Authorizer) *AuthInterceptor {
	return &AuthInterceptor{
		extractors:    make([]TokenExtractor, 0),
		authenticator: authenticator,
		authorizer:    authorizer,
		methodRules:   make(map[string][]string),
	}
}

// AddExtractor 添加令牌提取器
func (i *AuthInterceptor) AddExtractor(extractor TokenExtractor) *AuthInterceptor {
	i.extractors = append(i.extractors, extractor)
	return i
}

// AddMethodRule 添加方法权限规则
func (i *AuthInterceptor) AddMethodRule(method string, permissions ...string) *AuthInterceptor {
	i.methodRules[method] = permissions
	return i
}

// UnaryServerInterceptor 创建一元服务拦截器
func (i *AuthInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 判断方法是否需要认证
		permissions, needsAuth := i.methodRules[info.FullMethod]
		if !needsAuth {
			return handler(ctx, req)
		}

		// 提取令牌
		var creds Credentials
		var err error

		for _, extractor := range i.extractors {
			creds, err = extractor.ExtractToken(ctx)
			if err == nil && creds != nil {
				break
			}
		}

		if err != nil || creds == nil {
			return nil, ErrorToStatus(ErrUnauthenticated)
		}

		// 认证用户
		user, err := i.authenticator.Authenticate(ctx, creds)
		if err != nil {
			return nil, ErrorToStatus(err)
		}

		// 权限检查
		if len(permissions) > 0 && i.authorizer != nil {
			for _, perm := range permissions {
				allowed, err := i.authorizer.Authorize(ctx, user, info.FullMethod, perm)
				if err != nil {
					return nil, ErrorToStatus(err)
				}
				if !allowed {
					return nil, ErrorToStatus(ErrUnauthorized)
				}
			}
		}

		// 将用户信息添加到上下文
		newCtx := WithUser(ctx, user)

		return handler(newCtx, req)
	}
}

// StreamServerInterceptor 创建流服务拦截器
func (i *AuthInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 判断方法是否需要认证
		permissions, needsAuth := i.methodRules[info.FullMethod]
		if !needsAuth {
			return handler(srv, ss)
		}

		// 提取令牌
		ctx := ss.Context()
		var creds Credentials
		var err error

		for _, extractor := range i.extractors {
			creds, err = extractor.ExtractToken(ctx)
			if err == nil && creds != nil {
				break
			}
		}

		if err != nil || creds == nil {
			return ErrorToStatus(ErrUnauthenticated)
		}

		// 认证用户
		user, err := i.authenticator.Authenticate(ctx, creds)
		if err != nil {
			return ErrorToStatus(err)
		}

		// 权限检查
		if len(permissions) > 0 && i.authorizer != nil {
			for _, perm := range permissions {
				allowed, err := i.authorizer.Authorize(ctx, user, info.FullMethod, perm)
				if err != nil {
					return ErrorToStatus(err)
				}
				if !allowed {
					return ErrorToStatus(ErrUnauthorized)
				}
			}
		}

		// 将用户信息添加到上下文
		newCtx := WithUser(ctx, user)

		// 包装流以传递新的上下文
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          newCtx,
		}

		return handler(srv, wrappedStream)
	}
}

// wrappedServerStream 包装grpc.ServerStream以传递修改后的上下文
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context 返回修改后的上下文
func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}
