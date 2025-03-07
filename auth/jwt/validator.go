package jwt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/dormoron/eidola/auth"
	jwt "github.com/golang-jwt/jwt/v5"
)

// TokenValidator JWT令牌验证器接口
type TokenValidator interface {
	// Validate 验证令牌
	Validate(ctx context.Context, tokenString string) (auth.Claims, error)
}

// ValidationRule 验证规则函数类型
type ValidationRule func(ctx context.Context, claims jwt.Claims) error

// EnhancedValidator 增强的JWT验证器
type EnhancedValidator struct {
	// 基础令牌管理器
	tokenManager *JWTManager
	// 黑名单
	blacklist *TokenBlacklist
	// 验证规则列表
	rules []ValidationRule
}

// NewEnhancedValidator 创建新的增强验证器
func NewEnhancedValidator(tokenManager *JWTManager, blacklist *TokenBlacklist) *EnhancedValidator {
	validator := &EnhancedValidator{
		tokenManager: tokenManager,
		blacklist:    blacklist,
		rules:        make([]ValidationRule, 0),
	}

	// 添加默认验证规则
	validator.AddRule(NotBeforeValidator())
	validator.AddRule(ExpiryValidator())
	validator.AddRule(IssuerValidator(tokenManager.config.Issuer))

	if tokenManager.config.Audience != "" {
		validator.AddRule(AudienceValidator(tokenManager.config.Audience))
	}

	return validator
}

// AddRule 添加验证规则
func (v *EnhancedValidator) AddRule(rule ValidationRule) *EnhancedValidator {
	v.rules = append(v.rules, rule)
	return v
}

// Validate 验证令牌
func (v *EnhancedValidator) Validate(ctx context.Context, tokenString string) (auth.Claims, error) {
	// 先检查令牌是否在黑名单中
	if v.blacklist != nil {
		blacklisted, err := v.blacklist.Contains(ctx, tokenString)
		if err != nil {
			return nil, err
		}

		if blacklisted {
			return nil, ErrTokenBlacklisted
		}
	}

	// 基础验证
	authClaims, err := v.tokenManager.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	// 将auth.Claims转换为jwt.Claims以便应用规则
	jwtClaims := &JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{},
	}

	// 填充标准字段
	if exp, ok := authClaims["exp"].(*jwt.NumericDate); ok {
		jwtClaims.ExpiresAt = exp
	}
	if iat, ok := authClaims["iat"].(*jwt.NumericDate); ok {
		jwtClaims.IssuedAt = iat
	}
	if nbf, ok := authClaims["nbf"].(*jwt.NumericDate); ok {
		jwtClaims.NotBefore = nbf
	}
	if iss, ok := authClaims["iss"].(string); ok {
		jwtClaims.Issuer = iss
	}
	if aud, ok := authClaims["aud"].([]string); ok {
		jwtClaims.Audience = aud
	} else if aud, ok := authClaims["aud"].(string); ok {
		jwtClaims.Audience = []string{aud}
	}

	// 应用所有验证规则
	for _, rule := range v.rules {
		if err := rule(ctx, jwtClaims); err != nil {
			return nil, err
		}
	}

	return authClaims, nil
}

// 标准验证规则

// NotBeforeValidator 创建"not before"验证规则
func NotBeforeValidator() ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		nbf, err := claims.GetNotBefore()
		if err != nil {
			return nil // 没有nbf字段，跳过
		}

		if time.Now().Before(nbf.Time) {
			return errors.New("token not valid yet")
		}

		return nil
	}
}

// ExpiryValidator 创建过期验证规则
func ExpiryValidator() ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		exp, err := claims.GetExpirationTime()
		if err != nil {
			return nil // 没有exp字段，跳过
		}

		if time.Now().After(exp.Time) {
			return auth.ErrTokenExpired
		}

		return nil
	}
}

// IssuerValidator 创建颁发者验证规则
func IssuerValidator(expectedIssuer string) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		iss, err := claims.GetIssuer()
		if err != nil || iss == "" {
			return nil // 没有iss字段，跳过
		}

		if iss != expectedIssuer {
			return fmt.Errorf("invalid issuer: expected %s, got %s", expectedIssuer, iss)
		}

		return nil
	}
}

// AudienceValidator 创建受众验证规则
func AudienceValidator(expectedAudience string) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		aud, err := claims.GetAudience()
		if err != nil || len(aud) == 0 {
			return nil // 没有aud字段，跳过
		}

		valid := false
		for _, a := range aud {
			if a == expectedAudience {
				valid = true
				break
			}
		}

		if !valid {
			return fmt.Errorf("invalid audience: expected %s, got %v", expectedAudience, aud)
		}

		return nil
	}
}

// 高级验证规则

// SubjectValidator 创建主题验证规则
func SubjectValidator(expectedSubject string) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		sub, err := claims.GetSubject()
		if err != nil || sub == "" {
			return nil // 没有sub字段，跳过
		}

		if sub != expectedSubject {
			return fmt.Errorf("invalid subject: expected %s, got %s", expectedSubject, sub)
		}

		return nil
	}
}

// JWTIDValidator 创建JWT ID验证规则
func JWTIDValidator(expectedJTI string) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		// 尝试将claims转换为JWTClaims
		jwtClaims, ok := claims.(*JWTClaims)
		if !ok {
			return nil // 不是JWTClaims类型，跳过
		}

		// 获取JWT ID
		jti := jwtClaims.ID
		if jti == "" {
			return nil // 没有jti字段，跳过
		}

		if jti != expectedJTI {
			return fmt.Errorf("invalid JWT ID: expected %s, got %s", expectedJTI, jti)
		}

		return nil
	}
}

// IssuedAtValidator 创建签发时间验证规则
func IssuedAtValidator(maxAge time.Duration) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		iat, err := claims.GetIssuedAt()
		if err != nil {
			return nil // 没有iat字段，跳过
		}

		// 检查令牌是否太旧
		if time.Since(iat.Time) > maxAge {
			return errors.New("token too old")
		}

		return nil
	}
}

// IPAddressValidator 创建IP地址验证规则
func IPAddressValidator(allowedIPs ...string) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		// 获取上下文中的客户端IP
		var clientIP string
		if md, ok := ctx.Value("metadata").(map[string]interface{}); ok {
			if ip, ok := md["client_ip"].(string); ok {
				clientIP = ip
			}
		}

		if clientIP == "" {
			return nil // 无法获取客户端IP，跳过
		}

		// 检查IP是否在允许列表中
		for _, allowedIP := range allowedIPs {
			if strings.Contains(allowedIP, "/") {
				// CIDR格式
				_, ipNet, err := net.ParseCIDR(allowedIP)
				if err != nil {
					continue
				}

				ip := net.ParseIP(clientIP)
				if ip != nil && ipNet.Contains(ip) {
					return nil
				}
			} else {
				// 精确匹配
				if clientIP == allowedIP {
					return nil
				}
			}
		}

		return fmt.Errorf("client IP %s not allowed", clientIP)
	}
}

// CustomClaimValidator 创建自定义声明验证规则
func CustomClaimValidator(key string, expectedValue interface{}, required bool) ValidationRule {
	return func(ctx context.Context, claims jwt.Claims) error {
		// 尝试将claims转换为JWTClaims
		jwtClaims, ok := claims.(*JWTClaims)
		if !ok {
			if required {
				return fmt.Errorf("required claim %s not found", key)
			}
			return nil
		}

		// 检查Metadata中是否存在声明
		if jwtClaims.Metadata != nil {
			if value, ok := jwtClaims.Metadata[key]; ok {
				// 检查值是否匹配
				if value == expectedValue {
					return nil
				}
				return fmt.Errorf("invalid claim value for %s: expected %v, got %v", key, expectedValue, value)
			}
		}

		// 检查是否为标准字段
		switch key {
		case "uid":
			if jwtClaims.UserID == expectedValue {
				return nil
			}
			if required {
				return fmt.Errorf("invalid user ID: expected %v, got %v", expectedValue, jwtClaims.UserID)
			}
		case "username":
			if jwtClaims.Username == expectedValue {
				return nil
			}
			if required {
				return fmt.Errorf("invalid username: expected %v, got %v", expectedValue, jwtClaims.Username)
			}
		case "roles":
			// 检查角色列表
			expectedRoles, ok := expectedValue.([]string)
			if !ok {
				return fmt.Errorf("invalid expected roles format")
			}

			for _, role := range expectedRoles {
				found := false
				for _, r := range jwtClaims.Roles {
					if r == role {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("required role %s not found", role)
				}
			}
			return nil
		case "permissions":
			// 检查权限列表
			expectedPerms, ok := expectedValue.([]string)
			if !ok {
				return fmt.Errorf("invalid expected permissions format")
			}

			for _, perm := range expectedPerms {
				found := false
				for _, p := range jwtClaims.Permissions {
					if p == perm {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("required permission %s not found", perm)
				}
			}
			return nil
		}

		if required {
			return fmt.Errorf("required claim %s not found", key)
		}

		return nil
	}
}

// EnhancedJWTAuthenticator 带增强验证的JWT认证器
type EnhancedJWTAuthenticator struct {
	validator *EnhancedValidator
}

// NewEnhancedJWTAuthenticator 创建新的增强JWT认证器
func NewEnhancedJWTAuthenticator(validator *EnhancedValidator) *EnhancedJWTAuthenticator {
	return &EnhancedJWTAuthenticator{
		validator: validator,
	}
}

// Authenticate 验证凭证并返回用户信息
func (a *EnhancedJWTAuthenticator) Authenticate(ctx context.Context, creds auth.Credentials) (*auth.User, error) {
	// 验证令牌
	claims, err := a.validator.Validate(ctx, creds.GetToken())
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
