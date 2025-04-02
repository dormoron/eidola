package mfa

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dormoron/eidola/auth"
)

var (
	// ErrMFARequired 表示需要MFA认证
	ErrMFARequired = errors.New("需要进行多因素认证")
	// ErrMFAInvalid 表示MFA验证失败
	ErrMFAInvalid = errors.New("多因素认证验证失败")
	// ErrMFANotEnabled 表示未启用MFA
	ErrMFANotEnabled = errors.New("未启用多因素认证")
	// ErrUserNotFound 表示用户未找到
	ErrUserNotFound = errors.New("用户未找到")
)

// MFAType 多因素认证类型
type MFAType string

const (
	// MFATypeNone 无MFA
	MFATypeNone MFAType = "none"
	// MFATypeTOTP 基于时间的一次性密码
	MFATypeTOTP MFAType = "totp"
	// MFATypeSMS 短信验证码
	MFATypeSMS MFAType = "sms"
	// MFATypeEmail 邮件验证码
	MFATypeEmail MFAType = "email"
)

// MFAProvider 多因素认证提供者
type MFAProvider interface {
	// ProviderType 提供者类型
	ProviderType() MFAType
	// GenerateChallenge 生成挑战
	GenerateChallenge(ctx context.Context, userID string) (string, error)
	// VerifyChallenge 验证挑战
	VerifyChallenge(ctx context.Context, userID string, response string) (bool, error)
	// Setup 设置MFA
	Setup(ctx context.Context, userID string, params map[string]string) (map[string]string, error)
	// Disable 禁用MFA
	Disable(ctx context.Context, userID string) error
}

// TOTPProvider TOTP提供者
type TOTPProvider struct {
	// 存储用户TOTP密钥的仓库
	repository MFARepository
	// 验证窗口
	window int
	// 默认配置
	defaultConfig TOTPConfig
}

// MFARepository 多因素认证仓库
type MFARepository interface {
	// GetUserMFA 获取用户MFA设置
	GetUserMFA(ctx context.Context, userID string, mfaType MFAType) (map[string]string, error)
	// SetUserMFA 设置用户MFA
	SetUserMFA(ctx context.Context, userID string, mfaType MFAType, data map[string]string) error
	// DisableUserMFA 禁用用户MFA
	DisableUserMFA(ctx context.Context, userID string, mfaType MFAType) error
	// ListUserMFA 列出用户启用的MFA类型
	ListUserMFA(ctx context.Context, userID string) ([]MFAType, error)
}

// NewTOTPProvider 创建TOTP提供者
func NewTOTPProvider(repository MFARepository, issuer string, window int) *TOTPProvider {
	if window <= 0 {
		window = 1 // 默认验证窗口为前一个、当前和后一个时间段
	}

	return &TOTPProvider{
		repository: repository,
		window:     window,
		defaultConfig: TOTPConfig{
			Digits:    DefaultDigits,
			Period:    DefaultPeriod,
			Algorithm: DefaultAlgorithm,
			Issuer:    issuer,
		},
	}
}

// ProviderType 实现MFAProvider.ProviderType
func (p *TOTPProvider) ProviderType() MFAType {
	return MFATypeTOTP
}

// GenerateChallenge 实现MFAProvider.GenerateChallenge
// 对于TOTP不需要生成挑战，用户使用自己的认证器生成
func (p *TOTPProvider) GenerateChallenge(ctx context.Context, userID string) (string, error) {
	// 检查用户是否启用了TOTP
	data, err := p.repository.GetUserMFA(ctx, userID, MFATypeTOTP)
	if err != nil {
		return "", err
	}
	if data == nil || data["secret"] == "" {
		return "", ErrMFANotEnabled
	}

	// TOTP不需要发送挑战，返回空字符串即可
	return "", nil
}

// VerifyChallenge 实现MFAProvider.VerifyChallenge
func (p *TOTPProvider) VerifyChallenge(ctx context.Context, userID string, response string) (bool, error) {
	// 获取用户TOTP配置
	data, err := p.repository.GetUserMFA(ctx, userID, MFATypeTOTP)
	if err != nil {
		return false, err
	}
	if data == nil || data["secret"] == "" {
		return false, ErrMFANotEnabled
	}

	// 创建TOTP配置
	config := TOTPConfig{
		Secret:    data["secret"],
		Digits:    p.defaultConfig.Digits,
		Period:    p.defaultConfig.Period,
		Algorithm: p.defaultConfig.Algorithm,
		Issuer:    p.defaultConfig.Issuer,
	}

	// 创建TOTP
	totp, err := NewTOTP(&config)
	if err != nil {
		return false, err
	}

	// 验证TOTP
	return totp.ValidateCodeWithWindow(response, p.window), nil
}

// Setup 实现MFAProvider.Setup
func (p *TOTPProvider) Setup(ctx context.Context, userID string, params map[string]string) (map[string]string, error) {
	// 检查用户是否已经设置了TOTP
	existing, err := p.repository.GetUserMFA(ctx, userID, MFATypeTOTP)
	if err != nil && err != ErrUserNotFound {
		return nil, err
	}

	// 如果已设置且不是重置，返回错误
	if existing != nil && existing["secret"] != "" && params["reset"] != "true" {
		return nil, fmt.Errorf("TOTP已设置")
	}

	// 创建TOTP配置
	accountName := params["account_name"]
	if accountName == "" {
		accountName = userID
	}

	config := TOTPConfig{
		Secret:      "",
		Digits:      p.defaultConfig.Digits,
		Period:      p.defaultConfig.Period,
		Algorithm:   p.defaultConfig.Algorithm,
		Issuer:      p.defaultConfig.Issuer,
		AccountName: accountName,
	}

	// 创建TOTP
	totp, err := NewTOTP(&config)
	if err != nil {
		return nil, err
	}

	// 准备数据
	data := map[string]string{
		"secret":    totp.GetSecret(),
		"uri":       totp.GetURI(),
		"algorithm": p.defaultConfig.Algorithm,
		"digits":    fmt.Sprintf("%d", p.defaultConfig.Digits),
		"period":    fmt.Sprintf("%d", p.defaultConfig.Period),
	}

	// 如果是验证阶段
	if params["verify"] == "true" && params["code"] != "" {
		// 验证用户提供的OTP码
		valid := totp.ValidateCodeWithWindow(params["code"], 1)
		if !valid {
			return nil, ErrMFAInvalid
		}

		// 验证成功，保存TOTP设置
		err = p.repository.SetUserMFA(ctx, userID, MFATypeTOTP, data)
		if err != nil {
			return nil, err
		}

		data["setup_complete"] = "true"
	} else {
		// 初始设置阶段，返回密钥和URI
		data["setup_complete"] = "false"
	}

	return data, nil
}

// Disable 实现MFAProvider.Disable
func (p *TOTPProvider) Disable(ctx context.Context, userID string) error {
	return p.repository.DisableUserMFA(ctx, userID, MFATypeTOTP)
}

// InMemoryMFARepository 内存MFA仓库实现
type InMemoryMFARepository struct {
	// 用户MFA数据
	data map[string]map[string]map[string]string
	// 互斥锁
	mu sync.RWMutex
}

// NewInMemoryMFARepository 创建内存MFA仓库
func NewInMemoryMFARepository() *InMemoryMFARepository {
	return &InMemoryMFARepository{
		data: make(map[string]map[string]map[string]string),
	}
}

// GetUserMFA 实现MFARepository.GetUserMFA
func (r *InMemoryMFARepository) GetUserMFA(ctx context.Context, userID string, mfaType MFAType) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	userMFA, ok := r.data[userID]
	if !ok {
		return nil, ErrUserNotFound
	}

	mfaData, ok := userMFA[string(mfaType)]
	if !ok {
		return nil, ErrMFANotEnabled
	}

	return mfaData, nil
}

// SetUserMFA 实现MFARepository.SetUserMFA
func (r *InMemoryMFARepository) SetUserMFA(ctx context.Context, userID string, mfaType MFAType, data map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 如果用户不存在，初始化用户数据
	userMFA, ok := r.data[userID]
	if !ok {
		userMFA = make(map[string]map[string]string)
		r.data[userID] = userMFA
	}

	// 设置MFA数据
	userMFA[string(mfaType)] = data

	return nil
}

// DisableUserMFA 实现MFARepository.DisableUserMFA
func (r *InMemoryMFARepository) DisableUserMFA(ctx context.Context, userID string, mfaType MFAType) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	userMFA, ok := r.data[userID]
	if !ok {
		return nil // 用户不存在，无需禁用
	}

	delete(userMFA, string(mfaType))

	return nil
}

// ListUserMFA 实现MFARepository.ListUserMFA
func (r *InMemoryMFARepository) ListUserMFA(ctx context.Context, userID string) ([]MFAType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	userMFA, ok := r.data[userID]
	if !ok {
		return nil, ErrUserNotFound
	}

	var types []MFAType
	for t := range userMFA {
		types = append(types, MFAType(t))
	}

	return types, nil
}

// MFAAuthenticator 多因素认证器
type MFAAuthenticator struct {
	// 基础认证器
	baseAuthenticator auth.Authenticator
	// MFA提供者映射
	providers map[MFAType]MFAProvider
	// MFA仓库
	repository MFARepository
	// 是否强制MFA
	forceMFA bool
}

// MFAAuthenticatorConfig 多因素认证器配置
type MFAAuthenticatorConfig struct {
	// 基础认证器
	BaseAuthenticator auth.Authenticator
	// MFA提供者列表
	Providers []MFAProvider
	// MFA仓库
	Repository MFARepository
	// 是否强制MFA
	ForceMFA bool
}

// NewMFAAuthenticator 创建多因素认证器
func NewMFAAuthenticator(config MFAAuthenticatorConfig) *MFAAuthenticator {
	providerMap := make(map[MFAType]MFAProvider)
	for _, provider := range config.Providers {
		providerMap[provider.ProviderType()] = provider
	}

	return &MFAAuthenticator{
		baseAuthenticator: config.BaseAuthenticator,
		providers:         providerMap,
		repository:        config.Repository,
		forceMFA:          config.ForceMFA,
	}
}

// AuthenticationResult 认证结果
type AuthenticationResult struct {
	// 用户信息
	User *auth.User
	// 是否需要MFA
	RequireMFA bool
	// 可用的MFA类型
	AvailableMFATypes []MFAType
}

// PreAuthenticate 预认证（第一因素）
func (a *MFAAuthenticator) PreAuthenticate(ctx context.Context, credential auth.Credential) (*AuthenticationResult, error) {
	// 使用基础认证器验证凭证
	user, err := a.baseAuthenticator.Authenticate(ctx, credential)
	if err != nil {
		return nil, err
	}

	// 获取用户可用的MFA类型
	mfaTypes, err := a.repository.ListUserMFA(ctx, user.ID)
	if err != nil && err != ErrUserNotFound {
		return nil, err
	}

	// 检查是否需要MFA
	requireMFA := a.forceMFA || len(mfaTypes) > 0

	return &AuthenticationResult{
		User:              user,
		RequireMFA:        requireMFA,
		AvailableMFATypes: mfaTypes,
	}, nil
}

// CompleteMFA 完成MFA认证（第二因素）
func (a *MFAAuthenticator) CompleteMFA(ctx context.Context, userID string, mfaType MFAType, code string) (bool, error) {
	// 获取MFA提供者
	provider, ok := a.providers[mfaType]
	if !ok {
		return false, fmt.Errorf("不支持的MFA类型: %s", mfaType)
	}

	// 验证MFA
	return provider.VerifyChallenge(ctx, userID, code)
}

// SetupMFA 设置MFA
func (a *MFAAuthenticator) SetupMFA(ctx context.Context, userID string, mfaType MFAType, params map[string]string) (map[string]string, error) {
	// 获取MFA提供者
	provider, ok := a.providers[mfaType]
	if !ok {
		return nil, fmt.Errorf("不支持的MFA类型: %s", mfaType)
	}

	// 设置MFA
	return provider.Setup(ctx, userID, params)
}

// DisableMFA 禁用MFA
func (a *MFAAuthenticator) DisableMFA(ctx context.Context, userID string, mfaType MFAType) error {
	// 获取MFA提供者
	provider, ok := a.providers[mfaType]
	if !ok {
		return fmt.Errorf("不支持的MFA类型: %s", mfaType)
	}

	// 禁用MFA
	return provider.Disable(ctx, userID)
}

// Authenticate 实现auth.Authenticator接口
// 注意：这个方法只会执行基础认证，不会进行MFA验证
// 对于需要MFA的场景，应该先调用PreAuthenticate，然后调用CompleteMFA
func (a *MFAAuthenticator) Authenticate(ctx context.Context, credential auth.Credential) (*auth.User, error) {
	result, err := a.PreAuthenticate(ctx, credential)
	if err != nil {
		return nil, err
	}

	// 如果需要MFA，返回错误
	if result.RequireMFA {
		return nil, ErrMFARequired
	}

	return result.User, nil
}
