package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultPeriod 默认时间步长（秒）
	DefaultPeriod = 30
	// DefaultDigits 默认OTP位数
	DefaultDigits = 6
	// DefaultAlgorithm 默认算法
	DefaultAlgorithm = "SHA1"
)

// TOTPConfig TOTP配置
type TOTPConfig struct {
	// Secret 密钥
	Secret string
	// Digits OTP位数
	Digits int
	// Period 时间步长（秒）
	Period int
	// Algorithm 算法
	Algorithm string
	// Issuer 颁发者
	Issuer string
	// AccountName 账户名
	AccountName string
}

// NewDefaultTOTPConfig 创建默认TOTP配置
func NewDefaultTOTPConfig(issuer, accountName string) *TOTPConfig {
	return &TOTPConfig{
		Secret:      "",
		Digits:      DefaultDigits,
		Period:      DefaultPeriod,
		Algorithm:   DefaultAlgorithm,
		Issuer:      issuer,
		AccountName: accountName,
	}
}

// GenerateSecret 生成随机密钥
func GenerateSecret(length int) (string, error) {
	if length <= 0 {
		length = 20
	}

	// 生成随机字节
	secret := make([]byte, length)
	_, err := rand.Read(secret)
	if err != nil {
		return "", fmt.Errorf("生成随机密钥失败: %w", err)
	}

	// Base32编码
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// TOTP 实现基于时间的一次性密码
type TOTP struct {
	config *TOTPConfig
}

// NewTOTP 创建TOTP
func NewTOTP(config *TOTPConfig) (*TOTP, error) {
	if config == nil {
		return nil, fmt.Errorf("配置不能为空")
	}

	// 如果未提供密钥，生成一个
	if config.Secret == "" {
		secret, err := GenerateSecret(20)
		if err != nil {
			return nil, err
		}
		config.Secret = secret
	}

	// 标准化密钥（移除空格和填充）
	config.Secret = strings.ToUpper(strings.ReplaceAll(config.Secret, " ", ""))
	config.Secret = strings.TrimRight(config.Secret, "=")

	// 验证配置
	if config.Digits <= 0 {
		config.Digits = DefaultDigits
	}
	if config.Period <= 0 {
		config.Period = DefaultPeriod
	}
	if config.Algorithm == "" {
		config.Algorithm = DefaultAlgorithm
	}

	return &TOTP{
		config: config,
	}, nil
}

// GetSecret 获取密钥
func (t *TOTP) GetSecret() string {
	return t.config.Secret
}

// GenerateCode 生成当前OTP码
func (t *TOTP) GenerateCode() (string, error) {
	return t.GenerateCodeAt(time.Now())
}

// GenerateCodeAt 在指定时间生成OTP码
func (t *TOTP) GenerateCodeAt(at time.Time) (string, error) {
	// 计算时间计数器
	counter := uint64(math.Floor(float64(at.Unix()) / float64(t.config.Period)))

	// 生成OTP
	return t.generateOTP(counter)
}

// GenerateWithOffset 生成包含时间偏移的OTP码
func (t *TOTP) GenerateWithOffset(offset int) (string, error) {
	// 计算偏移时间
	offsetTime := time.Now().Add(time.Duration(offset) * time.Second)
	return t.GenerateCodeAt(offsetTime)
}

// ValidateCode 验证OTP码
func (t *TOTP) ValidateCode(code string) bool {
	return t.ValidateCodeWithWindow(code, 0)
}

// ValidateCodeWithWindow 使用时间窗口验证OTP码
// window是正向和反向的时间窗口数（例如，window=1表示检查前一个、当前和后一个时间步长）
func (t *TOTP) ValidateCodeWithWindow(code string, window int) bool {
	if window < 0 {
		window = 0
	}

	// 净化输入码
	code = strings.TrimSpace(code)
	if len(code) != t.config.Digits {
		return false
	}

	// 验证当前时间窗口的码
	now := time.Now()
	for i := -window; i <= window; i++ {
		offsetTime := now.Add(time.Duration(i*t.config.Period) * time.Second)
		validCode, err := t.GenerateCodeAt(offsetTime)
		if err != nil {
			continue
		}
		if validCode == code {
			return true
		}
	}

	return false
}

// GetURI 获取OTP URI（用于生成二维码）
func (t *TOTP) GetURI() string {
	// 构建URI
	issuer := url.QueryEscape(t.config.Issuer)
	account := url.QueryEscape(t.config.AccountName)
	params := url.Values{}
	params.Add("secret", t.config.Secret)
	params.Add("issuer", t.config.Issuer)
	params.Add("algorithm", t.config.Algorithm)
	params.Add("digits", strconv.Itoa(t.config.Digits))
	params.Add("period", strconv.Itoa(t.config.Period))

	return fmt.Sprintf("otpauth://totp/%s:%s?%s", issuer, account, params.Encode())
}

// generateOTP 生成OTP码
func (t *TOTP) generateOTP(counter uint64) (string, error) {
	// 准备计数器字节
	counterBytes := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		counterBytes[i] = byte(counter & 0xff)
		counter >>= 8
	}

	// 解码密钥
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(t.config.Secret)
	if err != nil {
		return "", fmt.Errorf("解码密钥失败: %w", err)
	}

	// 计算HMAC
	h := hmac.New(sha1.New, key)
	h.Write(counterBytes)
	hash := h.Sum(nil)

	// 动态截断
	offset := hash[len(hash)-1] & 0xf
	binary := ((int(hash[offset]) & 0x7f) << 24) |
		((int(hash[offset+1]) & 0xff) << 16) |
		((int(hash[offset+2]) & 0xff) << 8) |
		(int(hash[offset+3]) & 0xff)

	// 生成OTP码
	otp := binary % int(math.Pow10(t.config.Digits))
	otpStr := fmt.Sprintf("%0*d", t.config.Digits, otp)

	return otpStr, nil
}

// QRCodeGenerator 接口定义二维码生成器
type QRCodeGenerator interface {
	// GenerateQRCode 生成二维码图像
	GenerateQRCode(uri string, size int) ([]byte, error)
}

// 示例使用方法:
/*
func main() {
	// 创建TOTP配置
	config := NewDefaultTOTPConfig("MyApp", "user@example.com")

	// 创建TOTP
	totp, err := NewTOTP(config)
	if err != nil {
		panic(err)
	}

	// 获取密钥和URI
	fmt.Println("Secret:", totp.GetSecret())
	fmt.Println("URI:", totp.GetURI())

	// 生成OTP码
	code, err := totp.GenerateCode()
	if err != nil {
		panic(err)
	}
	fmt.Println("Current OTP:", code)

	// 验证OTP码
	valid := totp.ValidateCode(code)
	fmt.Println("Valid:", valid)
}
*/
