package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
)

// 定义可能的错误
var (
	ErrInvalidKey         = errors.New("无效的加密密钥")
	ErrEncryptionFailed   = errors.New("字段加密失败")
	ErrDecryptionFailed   = errors.New("字段解密失败")
	ErrInvalidCiphertext  = errors.New("无效的密文格式")
	ErrInvalidMessageType = errors.New("无效的消息类型，无法处理")
	ErrFieldNotFound      = errors.New("找不到指定的字段")
)

// EncryptionTag 标记需要加密的字段标签名
const EncryptionTag = "secure"

// EncryptionTagValue 标记加密字段的值选项
const (
	// 完全加密
	EncryptFull = "encrypt"
	// 部分掩码 (如信用卡只显示后4位)
	EncryptMask = "mask"
	// 仅用于传输加密，存储时明文
	EncryptTransport = "transport"
)

// FieldEncryptor 敏感字段加密器接口
type FieldEncryptor interface {
	// Encrypt 加密消息中的敏感字段
	Encrypt(msg interface{}) error
	// Decrypt 解密消息中的敏感字段
	Decrypt(msg interface{}) error
	// RegisterType 注册一个需要处理的消息类型及其敏感字段
	RegisterType(msgType interface{}, fieldPaths []string)
	// SetKey 设置加密密钥
	SetKey(key []byte) error
}

// fieldEncryptorImpl 敏感字段加密器实现
type fieldEncryptorImpl struct {
	key         []byte                   // 加密密钥
	block       cipher.Block             // AES加密块
	typeFields  map[reflect.Type][]field // 类型到字段路径的映射
	typeFieldMu sync.RWMutex             // 保护typeFields的互斥锁
}

// field 表示要加密的字段
type field struct {
	path      []string // 字段路径，支持嵌套，如 "User.Address.Street"
	encOption string   // 加密选项: full, mask, transport
}

// NewFieldEncryptor 创建新的字段加密器
func NewFieldEncryptor(key []byte) (FieldEncryptor, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	// 标准化密钥长度为32字节 (256位)
	hashedKey := sha256.Sum256(key)

	// 创建AES加密块
	block, err := aes.NewCipher(hashedKey[:])
	if err != nil {
		return nil, err
	}

	return &fieldEncryptorImpl{
		key:        hashedKey[:],
		block:      block,
		typeFields: make(map[reflect.Type][]field),
	}, nil
}

// SetKey 设置加密密钥
func (fe *fieldEncryptorImpl) SetKey(key []byte) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}

	// 标准化密钥长度
	hashedKey := sha256.Sum256(key)

	// 创建新的AES加密块
	block, err := aes.NewCipher(hashedKey[:])
	if err != nil {
		return err
	}

	fe.key = hashedKey[:]
	fe.block = block
	return nil
}

// RegisterType 注册一个消息类型及其敏感字段
func (fe *fieldEncryptorImpl) RegisterType(msgType interface{}, fieldPaths []string) {
	t := reflect.TypeOf(msgType)

	// 获取类型的基础类型（如果是指针，获取其指向的类型）
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	fields := make([]field, 0, len(fieldPaths))

	// 从类型中查找字段，并解析加密选项
	for _, path := range fieldPaths {
		parts := strings.Split(path, ".")
		if len(parts) == 0 {
			continue
		}

		// 默认为完全加密
		encOption := EncryptFull

		// 检查最后一部分是否包含加密选项
		lastPart := parts[len(parts)-1]
		if idx := strings.Index(lastPart, ":"); idx != -1 {
			option := lastPart[idx+1:]
			parts[len(parts)-1] = lastPart[:idx]

			// 验证加密选项
			switch option {
			case EncryptFull, EncryptMask, EncryptTransport:
				encOption = option
			}
		}

		fields = append(fields, field{
			path:      parts,
			encOption: encOption,
		})
	}

	fe.typeFieldMu.Lock()
	fe.typeFields[t] = fields
	fe.typeFieldMu.Unlock()
}

// Encrypt 加密消息中的敏感字段
func (fe *fieldEncryptorImpl) Encrypt(msg interface{}) error {
	if msg == nil {
		return nil
	}

	val := reflect.ValueOf(msg)

	// 如果是指针，获取其指向的值
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	// 只处理结构体
	if val.Kind() != reflect.Struct {
		return ErrInvalidMessageType
	}

	// 获取类型
	t := val.Type()

	// 检查该类型是否已注册
	fe.typeFieldMu.RLock()
	fields, ok := fe.typeFields[t]
	fe.typeFieldMu.RUnlock()

	if !ok {
		// 尝试通过结构体标签自动查找需要加密的字段
		fields = fe.findSecureFields(t)

		// 缓存结果
		if len(fields) > 0 {
			fe.typeFieldMu.Lock()
			fe.typeFields[t] = fields
			fe.typeFieldMu.Unlock()
		}
	}

	// 没有找到需要加密的字段
	if len(fields) == 0 {
		return nil
	}

	// 处理每个敏感字段
	for _, field := range fields {
		// 跳过仅传输加密的字段 (在编码器拦截器中处理)
		if field.encOption == EncryptTransport {
			continue
		}

		// 获取字段值
		fieldVal, err := getFieldValue(val, field.path)
		if err != nil {
			continue // 跳过找不到的字段
		}

		// 只加密string类型
		if fieldVal.Kind() != reflect.String || !fieldVal.CanSet() {
			continue
		}

		plaintext := fieldVal.String()
		if plaintext == "" {
			continue
		}

		var ciphertext string

		switch field.encOption {
		case EncryptFull:
			// 完全加密
			ciphertext, err = fe.encryptValue(plaintext)
			if err != nil {
				return err
			}
		case EncryptMask:
			// 部分掩码
			ciphertext = fe.maskValue(plaintext)
		}

		// 设置加密后的值
		fieldVal.SetString(ciphertext)
	}

	return nil
}

// Decrypt 解密消息中的敏感字段
func (fe *fieldEncryptorImpl) Decrypt(msg interface{}) error {
	if msg == nil {
		return nil
	}

	val := reflect.ValueOf(msg)

	// 如果是指针，获取其指向的值
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	// 只处理结构体
	if val.Kind() != reflect.Struct {
		return ErrInvalidMessageType
	}

	// 获取类型
	t := val.Type()

	// 检查该类型是否已注册
	fe.typeFieldMu.RLock()
	fields, ok := fe.typeFields[t]
	fe.typeFieldMu.RUnlock()

	if !ok {
		// 尝试通过结构体标签自动查找需要解密的字段
		fields = fe.findSecureFields(t)

		// 缓存结果
		if len(fields) > 0 {
			fe.typeFieldMu.Lock()
			fe.typeFields[t] = fields
			fe.typeFieldMu.Unlock()
		}
	}

	// 没有找到需要解密的字段
	if len(fields) == 0 {
		return nil
	}

	// 处理每个敏感字段
	for _, field := range fields {
		// 跳过仅传输加密的字段 (在编码器拦截器中处理)
		if field.encOption == EncryptTransport {
			continue
		}

		// 跳过掩码字段，它们不需要解密
		if field.encOption == EncryptMask {
			continue
		}

		// 获取字段值
		fieldVal, err := getFieldValue(val, field.path)
		if err != nil {
			continue // 跳过找不到的字段
		}

		// 只解密string类型
		if fieldVal.Kind() != reflect.String || !fieldVal.CanSet() {
			continue
		}

		ciphertext := fieldVal.String()
		if ciphertext == "" || !strings.HasPrefix(ciphertext, "enc:") {
			continue
		}

		// 解密值
		plaintext, err := fe.decryptValue(ciphertext)
		if err != nil {
			return err
		}

		// 设置解密后的值
		fieldVal.SetString(plaintext)
	}

	return nil
}

// findSecureFields 通过结构体标签查找需要加密的字段
func (fe *fieldEncryptorImpl) findSecureFields(t reflect.Type) []field {
	var fields []field

	// 递归查找所有嵌套字段
	var findFields func(reflect.Type, []string)
	findFields = func(t reflect.Type, prefix []string) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)

			// 检查当前字段是否有secure标签
			tagValue := f.Tag.Get(EncryptionTag)
			if tagValue != "" {
				path := append(prefix, f.Name)
				fields = append(fields, field{
					path:      path,
					encOption: tagValue,
				})
			}

			// 如果是结构体字段，递归查找
			if f.Type.Kind() == reflect.Struct {
				newPrefix := append(prefix, f.Name)
				findFields(f.Type, newPrefix)
			}
		}
	}

	findFields(t, nil)
	return fields
}

// getFieldValue 根据路径获取字段值
func getFieldValue(val reflect.Value, path []string) (reflect.Value, error) {
	current := val

	for _, name := range path {
		// 如果当前值是指针，获取其指向的值
		if current.Kind() == reflect.Ptr {
			if current.IsNil() {
				return reflect.Value{}, ErrFieldNotFound
			}
			current = current.Elem()
		}

		// 确保当前值是结构体
		if current.Kind() != reflect.Struct {
			return reflect.Value{}, ErrFieldNotFound
		}

		// 获取字段
		field := current.FieldByName(name)
		if !field.IsValid() {
			return reflect.Value{}, ErrFieldNotFound
		}

		current = field
	}

	return current, nil
}

// encryptValue 加密单个字符串值
func (fe *fieldEncryptorImpl) encryptValue(plaintext string) (string, error) {
	// 创建GCM模式
	gcm, err := cipher.NewGCM(fe.block)
	if err != nil {
		return "", ErrEncryptionFailed
	}

	// 创建随机nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", ErrEncryptionFailed
	}

	// 加密
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// 转换为base64编码并添加前缀
	return "enc:" + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptValue 解密单个字符串值
func (fe *fieldEncryptorImpl) decryptValue(ciphertext string) (string, error) {
	// 确保格式正确
	if !strings.HasPrefix(ciphertext, "enc:") {
		return ciphertext, nil // 不是加密的值，直接返回
	}

	// 去除前缀
	data := ciphertext[4:]

	// 解码base64
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", ErrInvalidCiphertext
	}

	// 创建GCM模式
	gcm, err := cipher.NewGCM(fe.block)
	if err != nil {
		return "", ErrDecryptionFailed
	}

	// 确保数据长度足够
	if len(decoded) < gcm.NonceSize() {
		return "", ErrInvalidCiphertext
	}

	// 提取nonce
	nonce := decoded[:gcm.NonceSize()]
	ciphertextBytes := decoded[gcm.NonceSize():]

	// 解密
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", ErrDecryptionFailed
	}

	return string(plaintext), nil
}

// maskValue 掩码处理敏感信息
func (fe *fieldEncryptorImpl) maskValue(value string) string {
	length := len(value)

	if length <= 4 {
		return "****" // 对于短字符串，全部掩码
	}

	// 保留前两位和后四位，其余掩码
	visible := 6
	if length < visible {
		visible = length
	}

	masked := value[:2]
	for i := 2; i < length-4; i++ {
		masked += "*"
	}
	masked += value[length-4:]

	return masked
}

// FieldEncryptionInterceptor 创建用于gRPC的字段加密拦截器
func FieldEncryptionInterceptor(encryptor FieldEncryptor) interface{} {
	// 返回一个包含服务端和客户端拦截器的封装结构体
	return &fieldEncryptionInterceptors{
		encryptor: encryptor,
	}
}

// fieldEncryptionInterceptors 包含服务端和客户端拦截器
type fieldEncryptionInterceptors struct {
	encryptor FieldEncryptor
}

// UnaryServerInterceptor 服务端一元拦截器
func (f *fieldEncryptionInterceptors) UnaryServerInterceptor() interface{} {
	return func(ctx interface{}, req interface{}, info interface{}, handler interface{}) (interface{}, error) {
		// 解密请求
		if err := f.encryptor.Decrypt(req); err != nil {
			return nil, err
		}

		// 调用处理函数
		resp, err := handler.(func(interface{}, interface{}) (interface{}, error))(ctx, req)
		if err != nil {
			return nil, err
		}

		// 加密响应
		if err := f.encryptor.Encrypt(resp); err != nil {
			return nil, err
		}

		return resp, nil
	}
}

// UnaryClientInterceptor 客户端一元拦截器
func (f *fieldEncryptionInterceptors) UnaryClientInterceptor() interface{} {
	return func(ctx interface{}, method string, req, reply interface{}, cc interface{}, invoker interface{}, opts ...interface{}) error {
		// 加密请求
		if err := f.encryptor.Encrypt(req); err != nil {
			return err
		}

		// 调用远程方法
		err := invoker.(func(interface{}, string, interface{}, interface{}, interface{}, ...interface{}) error)(
			ctx, method, req, reply, cc, opts...,
		)
		if err != nil {
			return err
		}

		// 解密响应
		return f.encryptor.Decrypt(reply)
	}
}
