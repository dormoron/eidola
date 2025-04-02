package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// Encryptor 定义加密接口
type Encryptor interface {
	// Encrypt 对数据进行加密/签名
	Encrypt(data []byte) ([]byte, error)
	// Verify 验证数据签名
	Verify(data []byte, signature []byte) error
}

// HmacEncryptor 使用HMAC-SHA256实现签名
type HmacEncryptor struct {
	key []byte
}

// NewHmacEncryptor 创建HMAC加密器
func NewHmacEncryptor(key []byte) (*HmacEncryptor, error) {
	if len(key) == 0 {
		return nil, errors.New("密钥不能为空")
	}
	return &HmacEncryptor{key: key}, nil
}

// Encrypt 使用HMAC-SHA256对数据进行签名
func (e *HmacEncryptor) Encrypt(data []byte) ([]byte, error) {
	h := hmac.New(sha256.New, e.key)
	h.Write(data)
	return h.Sum(nil), nil
}

// Verify 验证HMAC-SHA256签名
func (e *HmacEncryptor) Verify(data []byte, signature []byte) error {
	expected, _ := e.Encrypt(data)
	if hmac.Equal(expected, signature) {
		return nil
	}
	return errors.New("签名验证失败")
}

// AesEncryptor 使用AES-GCM加密
type AesEncryptor struct {
	key []byte
}

// NewAesEncryptor 创建AES加密器
func NewAesEncryptor(key []byte) (*AesEncryptor, error) {
	// AES-256需要32字节密钥，AES-128需要16字节密钥
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, errors.New("AES密钥长度必须为16、24或32字节")
	}
	return &AesEncryptor{key: key}, nil
}

// Encrypt 使用AES-GCM对数据进行加密
func (e *AesEncryptor) Encrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}

	// 创建GCM模式
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// 创建随机nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	// 加密并生成认证标签
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

// Verify AES-GCM会在解密时进行验证
func (e *AesEncryptor) Verify(data []byte, signature []byte) error {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return err
	}

	// 创建GCM模式
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// 确保签名长度足够
	if len(signature) < gcm.NonceSize() {
		return errors.New("签名长度不足")
	}

	// 分离nonce和密文
	nonce := signature[:gcm.NonceSize()]
	ciphertext := signature[gcm.NonceSize():]

	// 尝试解密
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return errors.New("签名验证失败")
	}

	// 实际上我们只需要验证数据签名而不是解密数据
	// 但既然已经解密了，可以比较解密后的数据是否与原始数据相同
	if string(plaintext) != string(data) {
		return errors.New("数据验证失败")
	}

	return nil
}

// CompositeEncryptor 组合多个加密器，提供额外的安全层
type CompositeEncryptor struct {
	encryptors []Encryptor
}

// NewCompositeEncryptor 创建组合加密器
func NewCompositeEncryptor(encryptors ...Encryptor) *CompositeEncryptor {
	return &CompositeEncryptor{
		encryptors: encryptors,
	}
}

// Encrypt 使用所有加密器依次加密
func (e *CompositeEncryptor) Encrypt(data []byte) ([]byte, error) {
	result := data
	var err error

	for _, encryptor := range e.encryptors {
		result, err = encryptor.Encrypt(result)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

// Verify 验证组合加密
func (e *CompositeEncryptor) Verify(data []byte, signature []byte) error {
	// 注意：组合加密器的验证逻辑取决于加密方式
	// 这里只是一个简化的示例，实际实现可能更复杂

	// 如果只有一个加密器，直接验证
	if len(e.encryptors) == 1 {
		return e.encryptors[0].Verify(data, signature)
	}

	// 多个加密器的情况，需要实现更复杂的验证逻辑
	// 这里只是一个示例
	expectedSig, err := e.Encrypt(data)
	if err != nil {
		return err
	}

	// 比较签名
	if len(expectedSig) != len(signature) {
		return errors.New("签名长度不匹配")
	}

	// 时间恒定比较，防止时间侧信道攻击
	var diff byte
	for i := 0; i < len(expectedSig); i++ {
		diff |= expectedSig[i] ^ signature[i]
	}

	if diff != 0 {
		return errors.New("签名验证失败")
	}

	return nil
}
