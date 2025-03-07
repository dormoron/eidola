package basic

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/dormoron/eidola/auth"
	"google.golang.org/grpc/metadata"
)

const (
	// AuthTypeBasic 基本认证类型
	AuthTypeBasic = "Basic"

	// AuthHeaderKey 认证头键
	AuthHeaderKey = "authorization"
)

// UserStore 用户存储接口
type UserStore interface {
	// GetUserByUsername 根据用户名获取用户
	GetUserByUsername(username string) (*auth.User, error)

	// ValidateCredentials 验证用户凭证
	ValidateCredentials(username, password string) (bool, error)
}

// InMemoryUserStore 内存用户存储
type InMemoryUserStore struct {
	users     map[string]*auth.User
	passwords map[string]string
}

// NewInMemoryUserStore 创建新的内存用户存储
func NewInMemoryUserStore() *InMemoryUserStore {
	return &InMemoryUserStore{
		users:     make(map[string]*auth.User),
		passwords: make(map[string]string),
	}
}

// AddUser 添加用户
func (s *InMemoryUserStore) AddUser(username, password string, user *auth.User) {
	s.users[username] = user
	s.passwords[username] = password
}

// GetUserByUsername 根据用户名获取用户
func (s *InMemoryUserStore) GetUserByUsername(username string) (*auth.User, error) {
	user, ok := s.users[username]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

// ValidateCredentials 验证用户凭证
func (s *InMemoryUserStore) ValidateCredentials(username, password string) (bool, error) {
	storedPassword, ok := s.passwords[username]
	if !ok {
		return false, errors.New("user not found")
	}
	return storedPassword == password, nil
}

// BasicCredentials 基本认证凭证
type BasicCredentials struct {
	username string
	password string
}

// GetToken 获取令牌字符串
func (c *BasicCredentials) GetToken() string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.username, c.password)))
}

// GetType 获取令牌类型
func (c *BasicCredentials) GetType() string {
	return AuthTypeBasic
}

// GetUsername 获取用户名
func (c *BasicCredentials) GetUsername() string {
	return c.username
}

// GetPassword 获取密码
func (c *BasicCredentials) GetPassword() string {
	return c.password
}

// NewBasicCredentials 创建基本认证凭证
func NewBasicCredentials(username, password string) *BasicCredentials {
	return &BasicCredentials{
		username: username,
		password: password,
	}
}

// BasicAuthenticator 基本认证器
type BasicAuthenticator struct {
	userStore UserStore
}

// NewBasicAuthenticator 创建新的基本认证器
func NewBasicAuthenticator(userStore UserStore) *BasicAuthenticator {
	return &BasicAuthenticator{
		userStore: userStore,
	}
}

// Authenticate 验证凭证并返回用户信息
func (a *BasicAuthenticator) Authenticate(ctx context.Context, creds auth.Credentials) (*auth.User, error) {
	// 尝试转换为基本凭证
	basicCreds, ok := creds.(*BasicCredentials)
	if !ok {
		// 尝试从base64编码的令牌解析用户名和密码
		tokenStr := creds.GetToken()
		decodedBytes, err := base64.StdEncoding.DecodeString(tokenStr)
		if err != nil {
			return nil, fmt.Errorf("invalid basic auth token: %w", err)
		}

		parts := strings.SplitN(string(decodedBytes), ":", 2)
		if len(parts) != 2 {
			return nil, errors.New("invalid basic auth format")
		}

		basicCreds = &BasicCredentials{
			username: parts[0],
			password: parts[1],
		}
	}

	// 验证凭证
	valid, err := a.userStore.ValidateCredentials(basicCreds.username, basicCreds.password)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, errors.New("invalid credentials")
	}

	// 获取用户
	return a.userStore.GetUserByUsername(basicCreds.username)
}

// HeaderBasicExtractor 从HTTP头部提取基本认证
type HeaderBasicExtractor struct {
	headerKey string
}

// NewHeaderBasicExtractor 创建新的头部基本认证提取器
func NewHeaderBasicExtractor(headerKey string) *HeaderBasicExtractor {
	if headerKey == "" {
		headerKey = AuthHeaderKey
	}

	return &HeaderBasicExtractor{
		headerKey: headerKey,
	}
}

// ExtractToken 从上下文中提取令牌
func (e *HeaderBasicExtractor) ExtractToken(ctx context.Context) (auth.Credentials, error) {
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

	if len(parts) != 2 || strings.ToLower(parts[0]) != strings.ToLower(AuthTypeBasic) {
		return nil, fmt.Errorf("invalid authorization header format")
	}

	tokenValue := parts[1]
	decodedBytes, err := base64.StdEncoding.DecodeString(tokenValue)
	if err != nil {
		return nil, fmt.Errorf("invalid basic auth token: %w", err)
	}

	credentials := strings.SplitN(string(decodedBytes), ":", 2)
	if len(credentials) != 2 {
		return nil, fmt.Errorf("invalid basic auth format")
	}

	return NewBasicCredentials(credentials[0], credentials[1]), nil
}

// ClientBasicInterceptor 创建携带基本认证的客户端拦截器
func ClientBasicInterceptor(username, password string) auth.ClientInterceptor {
	return func(ctx context.Context) context.Context {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		// 创建基本认证头
		authValue := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", username, password)))

		// 存储新的认证信息
		md = md.Copy()
		md.Set(AuthHeaderKey, fmt.Sprintf("%s %s", AuthTypeBasic, authValue))

		return metadata.NewOutgoingContext(ctx, md)
	}
}
