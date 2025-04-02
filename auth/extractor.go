package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"
)

var (
	ErrNoTokenFound = errors.New("未找到令牌")
)

// MetadataTokenExtractor 从GRPC元数据中提取令牌
type MetadataTokenExtractor struct {
	// 元数据键名
	key string
	// 令牌前缀（如 "Bearer "）
	prefix string
}

// NewMetadataTokenExtractor 创建一个新的元数据令牌提取器
func NewMetadataTokenExtractor(key string, prefix string) *MetadataTokenExtractor {
	if key == "" {
		key = "authorization"
	}
	return &MetadataTokenExtractor{
		key:    key,
		prefix: prefix,
	}
}

// Extract 从GRPC上下文元数据中提取令牌
func (e *MetadataTokenExtractor) Extract(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ErrNoTokenFound
	}

	values := md.Get(e.key)
	if len(values) == 0 {
		return "", ErrNoTokenFound
	}

	token := values[0]
	if e.prefix != "" {
		if !strings.HasPrefix(token, e.prefix) {
			return "", fmt.Errorf("令牌格式不正确: 需要前缀 %s", e.prefix)
		}
		token = strings.TrimPrefix(token, e.prefix)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrNoTokenFound
	}

	return token, nil
}

// CompositeTokenExtractor 组合多个令牌提取器
type CompositeTokenExtractor struct {
	extractors []TokenExtractor
}

// NewCompositeTokenExtractor 创建一个组合令牌提取器
func NewCompositeTokenExtractor(extractors ...TokenExtractor) *CompositeTokenExtractor {
	return &CompositeTokenExtractor{
		extractors: extractors,
	}
}

// Extract 尝试使用所有提取器提取令牌，返回第一个成功提取的结果
func (e *CompositeTokenExtractor) Extract(ctx context.Context) (string, error) {
	var lastErr error
	for _, extractor := range e.extractors {
		token, err := extractor.Extract(ctx)
		if err == nil {
			return token, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrNoTokenFound
}

// ConstantTokenExtractor 始终返回一个常量令牌（主要用于测试）
type ConstantTokenExtractor struct {
	token string
}

// NewConstantTokenExtractor 创建一个常量令牌提取器
func NewConstantTokenExtractor(token string) *ConstantTokenExtractor {
	return &ConstantTokenExtractor{
		token: token,
	}
}

// Extract 返回常量令牌
func (e *ConstantTokenExtractor) Extract(ctx context.Context) (string, error) {
	if e.token == "" {
		return "", ErrNoTokenFound
	}
	return e.token, nil
}
