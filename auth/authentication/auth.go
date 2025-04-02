package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// AuthType 表示支持的认证类型
type AuthType int

const (
	// AuthNone 表示不需要认证
	AuthNone AuthType = iota
	// AuthBasic 表示基本认证
	AuthBasic
	// AuthJWT 表示JWT认证
	AuthJWT
	// AuthOAuth2 表示OAuth2认证
	AuthOAuth2
)

// Authenticator 是认证接口
type Authenticator interface {
	// Authenticate 执行认证
	Authenticate(ctx context.Context) (context.Context, error)
	// GetAuthType 返回认证类型
	GetAuthType() AuthType
	// Name 返回认证器名称
	Name() string
}

// AuthProvider 是认证提供者，负责创建和管理认证器
type AuthProvider struct {
	authenticators map[AuthType]Authenticator
	defaultAuth    AuthType
}

// NewAuthProvider 创建认证提供者
func NewAuthProvider() *AuthProvider {
	return &AuthProvider{
		authenticators: make(map[AuthType]Authenticator),
		defaultAuth:    AuthNone,
	}
}

// RegisterAuthenticator 注册认证器
func (p *AuthProvider) RegisterAuthenticator(a Authenticator) {
	p.authenticators[a.GetAuthType()] = a
}

// SetDefaultAuth 设置默认认证类型
func (p *AuthProvider) SetDefaultAuth(authType AuthType) {
	p.defaultAuth = authType
}

// GetAuthenticator 获取指定类型的认证器
func (p *AuthProvider) GetAuthenticator(authType AuthType) (Authenticator, error) {
	auth, ok := p.authenticators[authType]
	if !ok {
		return nil, fmt.Errorf("未找到认证类型: %v", authType)
	}
	return auth, nil
}

// GetDefaultAuthenticator 获取默认认证器
func (p *AuthProvider) GetDefaultAuthenticator() (Authenticator, error) {
	return p.GetAuthenticator(p.defaultAuth)
}

// UnaryServerInterceptor 创建用于拦截一元RPC的服务端拦截器
func (p *AuthProvider) UnaryServerInterceptor(authType AuthType) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		auth, err := p.GetAuthenticator(authType)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		newCtx, err := auth.Authenticate(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		return handler(newCtx, req)
	}
}

// StreamServerInterceptor 创建用于拦截流RPC的服务端拦截器
func (p *AuthProvider) StreamServerInterceptor(authType AuthType) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		auth, err := p.GetAuthenticator(authType)
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		newCtx, err := auth.Authenticate(ss.Context())
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		// 包装ServerStream以使用新的认证上下文
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          newCtx,
		}

		return handler(srv, wrappedStream)
	}
}

// wrappedServerStream 包装grpc.ServerStream以使用提供的上下文
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context 覆盖ServerStream的Context方法
func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// UnaryClientInterceptor 创建用于拦截一元RPC的客户端拦截器
func (p *AuthProvider) UnaryClientInterceptor(authType AuthType) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		auth, err := p.GetAuthenticator(authType)
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		newCtx, err := auth.Authenticate(ctx)
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		return invoker(newCtx, method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptor 创建用于拦截流RPC的客户端拦截器
func (p *AuthProvider) StreamClientInterceptor(authType AuthType) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		auth, err := p.GetAuthenticator(authType)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		newCtx, err := auth.Authenticate(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "认证失败: %v", err)
		}

		return streamer(newCtx, desc, cc, method, opts...)
	}
}

// ======================= 基本认证实现 =======================

// BasicAuthCredentials 表示基本认证凭据
type BasicAuthCredentials struct {
	Username string
	Password string
}

// BasicAuthenticator 是基本认证实现
type BasicAuthenticator struct {
	verify func(username, password string) bool
}

// NewBasicAuthenticator 创建基本认证器
func NewBasicAuthenticator(verify func(username, password string) bool) *BasicAuthenticator {
	return &BasicAuthenticator{
		verify: verify,
	}
}

// GetAuthType 实现Authenticator接口
func (a *BasicAuthenticator) GetAuthType() AuthType {
	return AuthBasic
}

// Name 实现Authenticator接口
func (a *BasicAuthenticator) Name() string {
	return "BasicAuth"
}

// Authenticate 实现Authenticator接口
func (a *BasicAuthenticator) Authenticate(ctx context.Context) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("无法从上下文获取元数据")
	}

	// 从元数据中获取认证信息
	authorization := md.Get("authorization")
	if len(authorization) == 0 {
		return nil, errors.New("缺少认证信息")
	}

	authHeader := authorization[0]
	if !strings.HasPrefix(authHeader, "Basic ") {
		return nil, errors.New("不是基本认证")
	}

	// 解码认证信息
	encoded := strings.TrimPrefix(authHeader, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("解码认证信息失败")
	}

	credentials := strings.SplitN(string(decoded), ":", 2)
	if len(credentials) != 2 {
		return nil, errors.New("格式错误的认证信息")
	}

	username := credentials[0]
	password := credentials[1]

	// 验证凭据
	if !a.verify(username, password) {
		return nil, errors.New("无效的用户名或密码")
	}

	// 将凭据添加到上下文
	newMD := metadata.Pairs(
		"authenticated", "true",
		"username", username,
	)

	// 合并元数据
	newCtx := metadata.NewIncomingContext(ctx, metadata.Join(md, newMD))
	return newCtx, nil
}

// ======================= JWT认证实现 =======================

// JWTClaims 表示JWT声明
type JWTClaims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// JWTAuthenticator 是JWT认证实现
type JWTAuthenticator struct {
	signingKey      []byte
	signingMethod   jwt.SigningMethod
	expiration      time.Duration
	issuer          string
	validationFunc  func(token *jwt.Token) (interface{}, error)
	claimsGenerator func(subject string, additionalClaims map[string]interface{}) jwt.Claims
}

// JWTOption 是JWTAuthenticator的选项
type JWTOption func(*JWTAuthenticator)

// WithSigningMethod 设置签名方法
func WithSigningMethod(method jwt.SigningMethod) JWTOption {
	return func(a *JWTAuthenticator) {
		a.signingMethod = method
	}
}

// WithExpiration 设置过期时间
func WithExpiration(expiration time.Duration) JWTOption {
	return func(a *JWTAuthenticator) {
		a.expiration = expiration
	}
}

// WithIssuer 设置颁发者
func WithIssuer(issuer string) JWTOption {
	return func(a *JWTAuthenticator) {
		a.issuer = issuer
	}
}

// WithCustomClaims 设置自定义声明生成器
func WithCustomClaims(generator func(subject string, additionalClaims map[string]interface{}) jwt.Claims) JWTOption {
	return func(a *JWTAuthenticator) {
		a.claimsGenerator = generator
	}
}

// WithCustomValidation 设置自定义验证函数
func WithCustomValidation(validationFunc func(token *jwt.Token) (interface{}, error)) JWTOption {
	return func(a *JWTAuthenticator) {
		a.validationFunc = validationFunc
	}
}

// NewJWTAuthenticator 创建JWT认证器
func NewJWTAuthenticator(signingKey []byte, opts ...JWTOption) *JWTAuthenticator {
	authenticator := &JWTAuthenticator{
		signingKey:    signingKey,
		signingMethod: jwt.SigningMethodHS256,
		expiration:    time.Hour * 24, // 默认1天
		issuer:        "eidola",
	}

	// 默认验证函数
	authenticator.validationFunc = func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("意外的签名方法: %v", token.Header["alg"])
		}
		return authenticator.signingKey, nil
	}

	// 默认声明生成器
	authenticator.claimsGenerator = func(subject string, additionalClaims map[string]interface{}) jwt.Claims {
		now := time.Now()
		claims := &JWTClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(now.Add(authenticator.expiration)),
				IssuedAt:  jwt.NewNumericDate(now),
				NotBefore: jwt.NewNumericDate(now),
				Issuer:    authenticator.issuer,
				Subject:   subject,
			},
		}

		// 添加额外声明
		if username, ok := additionalClaims["username"].(string); ok {
			claims.Username = username
		}
		if role, ok := additionalClaims["role"].(string); ok {
			claims.Role = role
		}

		return claims
	}

	// 应用选项
	for _, opt := range opts {
		opt(authenticator)
	}

	return authenticator
}

// GetAuthType 实现Authenticator接口
func (a *JWTAuthenticator) GetAuthType() AuthType {
	return AuthJWT
}

// Name 实现Authenticator接口
func (a *JWTAuthenticator) Name() string {
	return "JWT"
}

// Authenticate 实现Authenticator接口
func (a *JWTAuthenticator) Authenticate(ctx context.Context) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("无法从上下文获取元数据")
	}

	// 从元数据中获取认证信息
	authorization := md.Get("authorization")
	if len(authorization) == 0 {
		return nil, errors.New("缺少认证信息")
	}

	authHeader := authorization[0]
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, errors.New("不是Bearer令牌")
	}

	// 提取令牌
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	// 解析并验证令牌
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, a.validationFunc)
	if err != nil {
		return nil, fmt.Errorf("解析JWT令牌失败: %v", err)
	}

	if !token.Valid {
		return nil, errors.New("无效的JWT令牌")
	}

	// 获取声明
	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, errors.New("无法提取JWT声明")
	}

	// 将声明添加到上下文
	newMD := metadata.Pairs(
		"authenticated", "true",
		"username", claims.Username,
		"role", claims.Role,
		"subject", claims.Subject,
	)

	// 合并元数据
	newCtx := metadata.NewIncomingContext(ctx, metadata.Join(md, newMD))
	return newCtx, nil
}

// GenerateToken 生成JWT令牌
func (a *JWTAuthenticator) GenerateToken(subject string, additionalClaims map[string]interface{}) (string, error) {
	// 生成声明
	claims := a.claimsGenerator(subject, additionalClaims)

	// 创建令牌
	token := jwt.NewWithClaims(a.signingMethod, claims)

	// 签名令牌
	return token.SignedString(a.signingKey)
}

// ======================= OAuth2认证实现 =======================

// OAuth2Config 表示OAuth2配置
type OAuth2Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	Endpoint     oauth2.Endpoint
}

// OAuth2Authenticator 是OAuth2认证实现
type OAuth2Authenticator struct {
	config *oauth2.Config
	verify func(token *oauth2.Token) bool
}

// NewOAuth2Authenticator 创建OAuth2认证器
func NewOAuth2Authenticator(cfg OAuth2Config, verify func(token *oauth2.Token) bool) *OAuth2Authenticator {
	config := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
		Endpoint:     cfg.Endpoint,
	}

	return &OAuth2Authenticator{
		config: config,
		verify: verify,
	}
}

// GetAuthType 实现Authenticator接口
func (a *OAuth2Authenticator) GetAuthType() AuthType {
	return AuthOAuth2
}

// Name 实现Authenticator接口
func (a *OAuth2Authenticator) Name() string {
	return "OAuth2"
}

// Authenticate 实现Authenticator接口
func (a *OAuth2Authenticator) Authenticate(ctx context.Context) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("无法从上下文获取元数据")
	}

	// 从元数据中获取认证信息
	authorization := md.Get("authorization")
	if len(authorization) == 0 {
		return nil, errors.New("缺少认证信息")
	}

	authHeader := authorization[0]
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, errors.New("不是Bearer令牌")
	}

	// 提取令牌
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	token := &oauth2.Token{
		AccessToken: tokenString,
	}

	// 验证令牌
	if !a.verify(token) {
		return nil, errors.New("无效的OAuth2令牌")
	}

	// 将OAuth凭据添加到上下文
	// 使用自定义key替代oauth.TokenKey
	oauthTokenKey := struct{}{}
	newCtx := context.WithValue(ctx, oauthTokenKey, token)

	// 如果需要，可以从令牌中提取用户信息
	userInfo, err := a.getUserInfo(token)
	if err == nil {
		// 将用户信息添加到元数据
		newMD := metadata.Pairs(
			"authenticated", "true",
			"oauth_user_id", userInfo["id"].(string),
		)

		// 将用户名添加到元数据（如果存在）
		if name, ok := userInfo["name"].(string); ok {
			newMD.Append("username", name)
		}

		// 将电子邮件添加到元数据（如果存在）
		if email, ok := userInfo["email"].(string); ok {
			newMD.Append("email", email)
		}

		// 合并元数据
		newCtx = metadata.NewIncomingContext(newCtx, metadata.Join(md, newMD))
	}

	return newCtx, nil
}

// getUserInfo 从OAuth2提供商获取用户信息
func (a *OAuth2Authenticator) getUserInfo(token *oauth2.Token) (map[string]interface{}, error) {
	// 这是一个示例，实际上应该根据OAuth2提供商的API文档来实现
	// 此处假设用户信息端点为 https://api.example.com/userinfo

	client := a.config.Client(context.Background(), token)
	resp, err := client.Get("https://api.example.com/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var userInfo map[string]interface{}
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, err
	}

	return userInfo, nil
}

// GetAuthURL 获取授权URL
func (a *OAuth2Authenticator) GetAuthURL(state string) string {
	return a.config.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange 交换授权码获取令牌
func (a *OAuth2Authenticator) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return a.config.Exchange(ctx, code)
}

// TokenSource 获取令牌源
func (a *OAuth2Authenticator) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return a.config.TokenSource(ctx, token)
}
