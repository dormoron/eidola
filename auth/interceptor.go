package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ContextKey 是上下文中用户信息的键
type ContextKey string

const (
	// UserContextKey 是上下文中用户信息的键名
	UserContextKey ContextKey = "user"
)

// AuthInterceptor 是一个GRPC认证拦截器
type AuthInterceptor struct {
	tokenExtractor TokenExtractor
	tokenManager   TokenManager
	authorizer     Authorizer
	// 不需要认证的方法
	publicMethods map[string]bool
	// 方法到资源和动作的映射
	methodResourceMap map[string]ResourceAction
}

// ResourceAction 表示资源和动作
type ResourceAction struct {
	Resource string
	Action   string
}

// NewAuthInterceptor 创建一个新的认证拦截器
func NewAuthInterceptor(
	tokenExtractor TokenExtractor,
	tokenManager TokenManager,
	authorizer Authorizer,
) *AuthInterceptor {
	return &AuthInterceptor{
		tokenExtractor:    tokenExtractor,
		tokenManager:      tokenManager,
		authorizer:        authorizer,
		publicMethods:     make(map[string]bool),
		methodResourceMap: make(map[string]ResourceAction),
	}
}

// AddPublicMethod 添加一个不需要认证的方法
func (i *AuthInterceptor) AddPublicMethod(fullMethodName string) {
	i.publicMethods[fullMethodName] = true
}

// AddResourceMapping 添加方法到资源和动作的映射
func (i *AuthInterceptor) AddResourceMapping(fullMethodName, resource, action string) {
	i.methodResourceMap[fullMethodName] = ResourceAction{
		Resource: resource,
		Action:   action,
	}
}

// UnaryServerInterceptor 返回一个一元服务器拦截器
func (i *AuthInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// 检查是否是公开方法
		if i.publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		// 认证
		user, err := i.authenticate(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		// 授权
		if err := i.authorize(ctx, user, info.FullMethod); err != nil {
			return nil, status.Errorf(codes.PermissionDenied, "授权失败: %v", err)
		}

		// 将用户信息添加到上下文
		newCtx := context.WithValue(ctx, UserContextKey, user)

		// 调用处理器
		return handler(newCtx, req)
	}
}

// StreamServerInterceptor 返回一个流服务器拦截器
func (i *AuthInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// 获取上下文
		ctx := ss.Context()

		// 检查是否是公开方法
		if i.publicMethods[info.FullMethod] {
			return handler(srv, ss)
		}

		// 认证
		user, err := i.authenticate(ctx)
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		// 授权
		if err := i.authorize(ctx, user, info.FullMethod); err != nil {
			return status.Errorf(codes.PermissionDenied, "授权失败: %v", err)
		}

		// 将用户信息添加到上下文
		newCtx := context.WithValue(ctx, UserContextKey, user)
		wrappedStream := newContextServerStream(newCtx, ss)

		// 调用处理器
		return handler(srv, wrappedStream)
	}
}

// authenticate 验证用户身份
func (i *AuthInterceptor) authenticate(ctx context.Context) (*User, error) {
	// 提取令牌
	token, err := i.tokenExtractor.Extract(ctx)
	if err != nil {
		return nil, err
	}

	// 验证令牌
	user, err := i.tokenManager.ValidateToken(ctx, token)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// authorize 授权用户访问
func (i *AuthInterceptor) authorize(ctx context.Context, user *User, fullMethodName string) error {
	// 从方法名映射获取资源和动作
	resourceAction, ok := i.methodResourceMap[fullMethodName]
	if !ok {
		// 如果没有映射，使用方法名作为资源名
		parts := strings.Split(fullMethodName, "/")
		if len(parts) < 2 {
			return ErrPermissionDenied
		}

		serviceName := parts[0]
		methodName := parts[1]

		resourceAction = ResourceAction{
			Resource: serviceName,
			Action:   methodName,
		}
	}

	// 检查权限
	if i.authorizer != nil {
		allowed, err := i.authorizer.CheckPermission(ctx, user, resourceAction.Resource, resourceAction.Action)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPermissionDenied
		}
	}

	return nil
}

// GetUserFromContext 从上下文中获取用户信息
func GetUserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(UserContextKey).(*User)
	return user, ok
}

// contextServerStream 包装 grpc.ServerStream 以使用新的上下文
type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context 返回包装的上下文
func (css *contextServerStream) Context() context.Context {
	return css.ctx
}

// newContextServerStream 创建一个新的包含上下文的流
func newContextServerStream(ctx context.Context, stream grpc.ServerStream) grpc.ServerStream {
	return &contextServerStream{
		ServerStream: stream,
		ctx:          ctx,
	}
}
