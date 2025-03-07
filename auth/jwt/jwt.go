package jwt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dormoron/eidola/auth"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// TokenTypeBearer 表示Bearer类型的令牌
	TokenTypeBearer = "Bearer"

	// AuthHeaderKey 表示认证头的键
	AuthHeaderKey = "authorization"
)

// JWTConfig JWT配置选项
type JWTConfig struct {
	// SigningKey 签名密钥
	SigningKey []byte

	// SigningMethod 签名方法
	SigningMethod jwt.SigningMethod

	// Expiry 令牌过期时间
	Expiry time.Duration

	// RefreshExpiry 刷新令牌过期时间
	RefreshExpiry time.Duration

	// Issuer 令牌颁发者
	Issuer string

	// Audience 令牌受众
	Audience string
}

// DefaultJWTConfig 默认的JWT配置
var DefaultJWTConfig = JWTConfig{
	SigningMethod: jwt.SigningMethodHS256,
	Expiry:        time.Hour,
	RefreshExpiry: time.Hour * 24 * 7, // 7天
}

// JWTManager JWT令牌管理器
type JWTManager struct {
	config JWTConfig
}

// NewJWTManager 创建新的JWT管理器
func NewJWTManager(signingKey []byte, config ...JWTConfig) *JWTManager {
	cfg := DefaultJWTConfig
	if len(config) > 0 {
		cfg = config[0]
	}
	cfg.SigningKey = signingKey

	return &JWTManager{
		config: cfg,
	}
}

// JWTClaims JWT载荷
type JWTClaims struct {
	jwt.RegisteredClaims

	// UserID 用户ID
	UserID string `json:"uid,omitempty"`

	// Username 用户名
	Username string `json:"username,omitempty"`

	// Roles 用户角色
	Roles []string `json:"roles,omitempty"`

	// Permissions 用户权限
	Permissions []string `json:"permissions,omitempty"`

	// Metadata 额外的元数据
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// CreateToken 创建JWT令牌
func (m *JWTManager) CreateToken(user *auth.User, expiry time.Duration) (string, error) {
	if expiry <= 0 {
		expiry = m.config.Expiry
	}

	now := time.Now()
	expiresAt := now.Add(expiry)

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
	return token.SignedString(m.config.SigningKey)
}

// ValidateToken 验证JWT令牌
func (m *JWTManager) ValidateToken(tokenString string) (auth.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// 验证签名方法
		if token.Method != m.config.SigningMethod {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.config.SigningKey, nil
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

// RefreshToken 刷新JWT令牌
func (m *JWTManager) RefreshToken(tokenString string) (string, error) {
	claims, err := m.ValidateToken(tokenString)
	if err != nil && err != auth.ErrTokenExpired {
		return "", err
	}

	user := &auth.User{
		ID:          claims["uid"].(string),
		Name:        claims["username"].(string),
		Roles:       claims["roles"].([]string),
		Permissions: claims["permissions"].([]string),
		Metadata:    make(map[string]interface{}),
	}

	// 复制元数据
	for k, v := range claims {
		if k != "uid" && k != "username" && k != "roles" && k != "permissions" &&
			k != "exp" && k != "iat" && k != "nbf" && k != "iss" && k != "aud" {
			user.Metadata[k] = v
		}
	}

	return m.CreateToken(user, m.config.Expiry)
}

// JWTCredentials JWT凭证实现
type JWTCredentials struct {
	token string
}

// GetToken 获取令牌字符串
func (c *JWTCredentials) GetToken() string {
	return c.token
}

// GetType 获取令牌类型
func (c *JWTCredentials) GetType() string {
	return TokenTypeBearer
}

// NewJWTCredentials 创建新的JWT凭证
func NewJWTCredentials(token string) *JWTCredentials {
	return &JWTCredentials{token: token}
}

// JWTAuthenticator JWT认证器
type JWTAuthenticator struct {
	tokenManager *JWTManager
}

// NewJWTAuthenticator 创建新的JWT认证器
func NewJWTAuthenticator(tokenManager *JWTManager) *JWTAuthenticator {
	return &JWTAuthenticator{
		tokenManager: tokenManager,
	}
}

// Authenticate 验证凭证并返回用户信息
func (a *JWTAuthenticator) Authenticate(ctx context.Context, creds auth.Credentials) (*auth.User, error) {
	// 验证令牌
	claims, err := a.tokenManager.ValidateToken(creds.GetToken())
	if err != nil {
		return nil, err
	}

	// 从载荷中提取用户信息
	userID, _ := claims["uid"].(string)
	username, _ := claims["username"].(string)
	roles, _ := claims["roles"].([]string)
	permissions, _ := claims["permissions"].([]string)

	// 创建用户
	user := &auth.User{
		ID:          userID,
		Name:        username,
		Roles:       roles,
		Permissions: permissions,
		Metadata:    make(map[string]interface{}),
	}

	// 复制元数据
	for k, v := range claims {
		if k != "uid" && k != "username" && k != "roles" && k != "permissions" &&
			k != "exp" && k != "iat" && k != "nbf" && k != "iss" && k != "aud" {
			user.Metadata[k] = v
		}
	}

	// 将载荷添加到上下文
	ctx = auth.WithClaims(ctx, claims)

	return user, nil
}

// HeaderTokenExtractor 从HTTP头部提取令牌
type HeaderTokenExtractor struct {
	headerKey string
}

// NewHeaderTokenExtractor 创建新的头部令牌提取器
func NewHeaderTokenExtractor(headerKey string) *HeaderTokenExtractor {
	if headerKey == "" {
		headerKey = AuthHeaderKey
	}

	return &HeaderTokenExtractor{
		headerKey: headerKey,
	}
}

// ExtractToken 从上下文中提取令牌
func (e *HeaderTokenExtractor) ExtractToken(ctx context.Context) (auth.Credentials, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("metadata not found in context")
	}

	values := md.Get(e.headerKey)
	if len(values) == 0 {
		return nil, fmt.Errorf("authorization header not found")
	}

	authHeader := values[0]
	parts := strings.SplitN(authHeader, " ", 2)

	if len(parts) != 2 || strings.ToLower(parts[0]) != strings.ToLower(TokenTypeBearer) {
		return nil, fmt.Errorf("invalid authorization header format")
	}

	return NewJWTCredentials(parts[1]), nil
}

// ClientJWTInterceptor 创建携带JWT的客户端拦截器
func ClientJWTInterceptor(token string) auth.ClientInterceptor {
	return func(ctx context.Context) context.Context {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		// 存储新的认证信息
		md = md.Copy()
		md.Set(AuthHeaderKey, fmt.Sprintf("%s %s", TokenTypeBearer, token))

		return metadata.NewOutgoingContext(ctx, md)
	}
}

// JWTMiddleware 创建用于HTTP服务的JWT中间件
type JWTMiddleware struct {
	authenticator *JWTAuthenticator
	extractor     *HeaderTokenExtractor
	skipPaths     map[string]bool
}

// NewJWTMiddleware 创建新的JWT中间件
func NewJWTMiddleware(authenticator *JWTAuthenticator, extractor *HeaderTokenExtractor) *JWTMiddleware {
	return &JWTMiddleware{
		authenticator: authenticator,
		extractor:     extractor,
		skipPaths:     make(map[string]bool),
	}
}

// SkipPath 添加不需要认证的路径
func (m *JWTMiddleware) SkipPath(path string) *JWTMiddleware {
	m.skipPaths[path] = true
	return m
}

// ServerInterceptor 创建服务器拦截器
func (m *JWTMiddleware) ServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 检查是否跳过认证
		if m.skipPaths[info.FullMethod] {
			return handler(ctx, req)
		}

		// 提取令牌
		creds, err := m.extractor.ExtractToken(ctx)
		if err != nil {
			return nil, auth.ErrorToStatus(auth.ErrUnauthenticated)
		}

		// 认证用户
		user, err := m.authenticator.Authenticate(ctx, creds)
		if err != nil {
			return nil, auth.ErrorToStatus(err)
		}

		// 将用户信息添加到上下文
		newCtx := auth.WithUser(ctx, user)

		return handler(newCtx, req)
	}
}
