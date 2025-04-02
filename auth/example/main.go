package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dormoron/eidola/auth"
	"github.com/dormoron/eidola/auth/token"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// 示例服务接口定义
type UserService struct {
	UnimplementedUserServiceServer
}

type UnimplementedUserServiceServer struct{}

func (s *UnimplementedUserServiceServer) GetUser(ctx context.Context, req *GetUserRequest) (*User, error) {
	return nil, status.Errorf(codes.Unimplemented, "方法 GetUser 未实现")
}

func (s *UnimplementedUserServiceServer) UpdateUser(ctx context.Context, req *UpdateUserRequest) (*User, error) {
	return nil, status.Errorf(codes.Unimplemented, "方法 UpdateUser 未实现")
}

type GetUserRequest struct {
	UserId string
}

type UpdateUserRequest struct {
	User *User
}

type User struct {
	Id       string
	Username string
	Email    string
}

// UserServiceServer 是服务器API的接口
type UserServiceServer interface {
	GetUser(context.Context, *GetUserRequest) (*User, error)
	UpdateUser(context.Context, *UpdateUserRequest) (*User, error)
}

// 实现UserService
func (s *UserService) GetUser(ctx context.Context, req *GetUserRequest) (*User, error) {
	// 从上下文中获取用户信息
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "未授权访问")
	}

	log.Printf("用户 %s 获取用户ID %s 的信息", user.Username, req.UserId)

	// 返回用户信息
	return &User{
		Id:       req.UserId,
		Username: "testuser",
		Email:    "test@example.com",
	}, nil
}

func (s *UserService) UpdateUser(ctx context.Context, req *UpdateUserRequest) (*User, error) {
	// 从上下文中获取用户信息
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "未授权访问")
	}

	log.Printf("用户 %s 更新用户ID %s 的信息", user.Username, req.User.Id)

	// 更新用户信息
	return req.User, nil
}

// AuthService 认证服务
type AuthService struct {
	UnimplementedAuthServiceServer
	tokenService *token.TokenService
}

type UnimplementedAuthServiceServer struct{}

func (s *UnimplementedAuthServiceServer) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "方法 Login 未实现")
}

func (s *UnimplementedAuthServiceServer) Refresh(ctx context.Context, req *RefreshRequest) (*LoginResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "方法 Refresh 未实现")
}

func (s *UnimplementedAuthServiceServer) Logout(ctx context.Context, req *LogoutRequest) (*LogoutResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "方法 Logout 未实现")
}

type LoginRequest struct {
	Username string
	Password string
}

type LoginResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	TokenType    string
}

type RefreshRequest struct {
	RefreshToken string
}

type LogoutRequest struct{}

type LogoutResponse struct {
	Success bool
	Message string
}

// AuthServiceServer 是认证服务器API的接口
type AuthServiceServer interface {
	Login(context.Context, *LoginRequest) (*LoginResponse, error)
	Refresh(context.Context, *RefreshRequest) (*LoginResponse, error)
	Logout(context.Context, *LogoutRequest) (*LogoutResponse, error)
}

// 实现AuthService
func (s *AuthService) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	// 调用令牌服务登录
	tokenInfo, err := s.tokenService.Login(ctx, req.Username, req.Password)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "登录失败: %v", err)
	}

	// 返回令牌信息
	return &LoginResponse{
		AccessToken:  tokenInfo.AccessToken,
		RefreshToken: tokenInfo.RefreshToken,
		ExpiresIn:    tokenInfo.ExpiresAt.Unix() - time.Now().Unix(),
		TokenType:    tokenInfo.TokenType,
	}, nil
}

func (s *AuthService) Refresh(ctx context.Context, req *RefreshRequest) (*LoginResponse, error) {
	// 调用令牌服务刷新令牌
	tokenInfo, err := s.tokenService.Refresh(ctx, req.RefreshToken)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "刷新令牌失败: %v", err)
	}

	// 返回新令牌信息
	return &LoginResponse{
		AccessToken:  tokenInfo.AccessToken,
		RefreshToken: tokenInfo.RefreshToken,
		ExpiresIn:    tokenInfo.ExpiresAt.Unix() - time.Now().Unix(),
		TokenType:    tokenInfo.TokenType,
	}, nil
}

func (s *AuthService) Logout(ctx context.Context, req *LogoutRequest) (*LogoutResponse, error) {
	// 调用令牌服务登出
	err := s.tokenService.Logout(ctx)
	if err != nil {
		return &LogoutResponse{
			Success: false,
			Message: fmt.Sprintf("登出失败: %v", err),
		}, nil
	}

	return &LogoutResponse{
		Success: true,
		Message: "登出成功",
	}, nil
}

// 设置令牌服务
func setupTokenService() *token.TokenService {
	// 创建令牌存储
	tokenStore := token.NewInMemoryTokenStore()

	// 创建一个示例用户
	user := &auth.User{
		ID:          "1",
		Username:    "admin",
		Roles:       []string{"admin"},
		Permissions: []string{"*"},
	}
	tokenStore.StoreUser(user)

	user2 := &auth.User{
		ID:          "2",
		Username:    "user",
		Roles:       []string{"user"},
		Permissions: []string{"user:read"},
	}
	tokenStore.StoreUser(user2)

	// 创建密钥
	signingKey := []byte("your-secret-key-for-testing-only")

	// 创建加密器 - 可以选择HMAC或AES
	encryptor, _ := token.NewHmacEncryptor(signingKey)
	// 或者使用AES
	// encryptor, _ := token.NewAesEncryptor(signingKey)

	// 创建令牌管理器
	tokenManager, _ := token.NewCustomTokenManager(token.CustomTokenConfig{
		SigningKey:      signingKey,
		DefaultDuration: time.Hour,
		Issuer:          "eidola-example",
		Encryptor:       encryptor,
		TokenStore:      tokenStore,
	})

	// 创建用户认证函数
	authenticator := func(ctx context.Context, username, password string) (*auth.User, error) {
		// 在实际应用中，这里应该查询数据库验证用户凭证
		if username == "admin" && password == "admin123" {
			return user, nil
		} else if username == "user" && password == "user123" {
			return user2, nil
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
			AccessTokenDuration:        time.Minute * 15,
			RefreshTokenDuration:       time.Hour * 24,
			EnableRefreshTokenRotation: true,
			EnableTokenRevocation:      true,
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

// 创建GRPC服务器并注册服务
func setupGRPCServer(tokenService *token.TokenService) *grpc.Server {
	// 创建令牌提取器
	extractor := auth.NewMetadataTokenExtractor("authorization", "Bearer ")

	// 获取令牌管理器
	tokenManager := tokenService.GetTokenManager()

	// 创建授权器
	authorizer := auth.NewRBACAuthorizer()
	authorizer.AddRole("admin", []string{"*"})
	authorizer.AddRole("user", []string{"user:read"})

	// 创建认证拦截器
	interceptor := auth.NewAuthInterceptor(extractor, tokenManager, authorizer)

	// 添加公开方法
	interceptor.AddPublicMethod("/AuthService/Login")
	interceptor.AddPublicMethod("/AuthService/Refresh")

	// 添加资源映射
	interceptor.AddResourceMapping("/UserService/GetUser", "user", "read")
	interceptor.AddResourceMapping("/UserService/UpdateUser", "user", "write")

	// 创建GRPC服务器
	server := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor.UnaryServerInterceptor()),
		grpc.StreamInterceptor(interceptor.StreamServerInterceptor()),
	)

	// 注册服务
	RegisterUserServiceServer(server, &UserService{})
	RegisterAuthServiceServer(server, &AuthService{tokenService: tokenService})

	return server
}

// 模拟客户端使用示例
func clientExample() {
	// 创建一个没有认证的连接
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("连接服务器失败: %v", err)
	}
	defer conn.Close()

	// 创建认证服务客户端
	authClient := NewAuthServiceClient(conn)

	// 登录获取令牌
	ctx := context.Background()
	loginResp, err := authClient.Login(ctx, &LoginRequest{
		Username: "admin",
		Password: "admin123",
	})
	if err != nil {
		log.Fatalf("登录失败: %v", err)
	}

	fmt.Printf("登录成功! 令牌: %s\n", loginResp.AccessToken)

	// 创建带认证的上下文
	authCtx := metadata.AppendToOutgoingContext(
		context.Background(),
		"authorization", fmt.Sprintf("Bearer %s", loginResp.AccessToken),
	)

	// 创建用户服务客户端
	userClient := NewUserServiceClient(conn)

	// 获取用户信息
	user, err := userClient.GetUser(authCtx, &GetUserRequest{UserId: "1"})
	if err != nil {
		log.Fatalf("获取用户失败: %v", err)
	}

	fmt.Printf("获取用户成功: %+v\n", user)

	// 更新用户信息
	updatedUser, err := userClient.UpdateUser(authCtx, &UpdateUserRequest{
		User: &User{
			Id:       "1",
			Username: "newadmin",
			Email:    "admin@example.com",
		},
	})
	if err != nil {
		log.Fatalf("更新用户失败: %v", err)
	}

	fmt.Printf("更新用户成功: %+v\n", updatedUser)

	// 刷新令牌
	refreshResp, err := authClient.Refresh(ctx, &RefreshRequest{
		RefreshToken: loginResp.RefreshToken,
	})
	if err != nil {
		log.Fatalf("刷新令牌失败: %v", err)
	}

	fmt.Printf("刷新令牌成功! 新令牌: %s\n", refreshResp.AccessToken)

	// 登出
	logoutResp, err := authClient.Logout(authCtx, &LogoutRequest{})
	if err != nil {
		log.Fatalf("登出失败: %v", err)
	}

	fmt.Printf("登出结果: %+v\n", logoutResp)
}

// 客户端接口注册部分

// RegisterUserServiceServer registers the UserServiceServer to the grpc.Server
func RegisterUserServiceServer(s *grpc.Server, srv UserServiceServer) {
	s.RegisterService(&_UserService_serviceDesc, srv)
}

// RegisterAuthServiceServer registers the AuthServiceServer to the grpc.Server
func RegisterAuthServiceServer(s *grpc.Server, srv AuthServiceServer) {
	s.RegisterService(&_AuthService_serviceDesc, srv)
}

// 客户端接口定义
type UserServiceClient interface {
	GetUser(ctx context.Context, in *GetUserRequest, opts ...grpc.CallOption) (*User, error)
	UpdateUser(ctx context.Context, in *UpdateUserRequest, opts ...grpc.CallOption) (*User, error)
}

type userServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewUserServiceClient(cc grpc.ClientConnInterface) UserServiceClient {
	return &userServiceClient{cc}
}

func (c *userServiceClient) GetUser(ctx context.Context, in *GetUserRequest, opts ...grpc.CallOption) (*User, error) {
	out := new(User)
	err := c.cc.Invoke(ctx, "/UserService/GetUser", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *userServiceClient) UpdateUser(ctx context.Context, in *UpdateUserRequest, opts ...grpc.CallOption) (*User, error) {
	out := new(User)
	err := c.cc.Invoke(ctx, "/UserService/UpdateUser", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type AuthServiceClient interface {
	Login(ctx context.Context, in *LoginRequest, opts ...grpc.CallOption) (*LoginResponse, error)
	Refresh(ctx context.Context, in *RefreshRequest, opts ...grpc.CallOption) (*LoginResponse, error)
	Logout(ctx context.Context, in *LogoutRequest, opts ...grpc.CallOption) (*LogoutResponse, error)
}

type authServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewAuthServiceClient(cc grpc.ClientConnInterface) AuthServiceClient {
	return &authServiceClient{cc}
}

func (c *authServiceClient) Login(ctx context.Context, in *LoginRequest, opts ...grpc.CallOption) (*LoginResponse, error) {
	out := new(LoginResponse)
	err := c.cc.Invoke(ctx, "/AuthService/Login", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *authServiceClient) Refresh(ctx context.Context, in *RefreshRequest, opts ...grpc.CallOption) (*LoginResponse, error) {
	out := new(LoginResponse)
	err := c.cc.Invoke(ctx, "/AuthService/Refresh", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *authServiceClient) Logout(ctx context.Context, in *LogoutRequest, opts ...grpc.CallOption) (*LogoutResponse, error) {
	out := new(LogoutResponse)
	err := c.cc.Invoke(ctx, "/AuthService/Logout", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// 服务描述符
var _UserService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "UserService",
	HandlerType: (*UserServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetUser",
			Handler:    _UserService_GetUser_Handler,
		},
		{
			MethodName: "UpdateUser",
			Handler:    _UserService_UpdateUser_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "user_service.proto",
}

func _UserService_GetUser_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetUserRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(UserServiceServer).GetUser(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/UserService/GetUser",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(UserServiceServer).GetUser(ctx, req.(*GetUserRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _UserService_UpdateUser_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateUserRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(UserServiceServer).UpdateUser(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/UserService/UpdateUser",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(UserServiceServer).UpdateUser(ctx, req.(*UpdateUserRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _AuthService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "AuthService",
	HandlerType: (*AuthServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Login",
			Handler:    _AuthService_Login_Handler,
		},
		{
			MethodName: "Refresh",
			Handler:    _AuthService_Refresh_Handler,
		},
		{
			MethodName: "Logout",
			Handler:    _AuthService_Logout_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "auth_service.proto",
}

func _AuthService_Login_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(LoginRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AuthServiceServer).Login(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/AuthService/Login",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AuthServiceServer).Login(ctx, req.(*LoginRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _AuthService_Refresh_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RefreshRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AuthServiceServer).Refresh(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/AuthService/Refresh",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AuthServiceServer).Refresh(ctx, req.(*RefreshRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _AuthService_Logout_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(LogoutRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AuthServiceServer).Logout(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/AuthService/Logout",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AuthServiceServer).Logout(ctx, req.(*LogoutRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func main() {
	// 设置令牌服务
	tokenService := setupTokenService()

	// 设置GRPC服务器
	server := setupGRPCServer(tokenService)

	// 监听端口
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}

	// 启动服务器
	go func() {
		log.Println("启动GRPC服务器在 :50051")
		if err := server.Serve(lis); err != nil {
			log.Fatalf("服务器运行失败: %v", err)
		}
	}()

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// 阻塞直到收到信号
	<-sigCh

	// 优雅关闭
	log.Println("关闭服务器...")
	server.GracefulStop()
	log.Println("服务器已关闭")
}
