package redis

import "errors"

// 错误定义
var (
	// ErrNilRedisClient Redis客户端为空错误
	ErrNilRedisClient = errors.New("Redis客户端不能为空")
	// ErrRedisConnFailed Redis连接失败
	ErrRedisConnFailed = errors.New("Redis连接失败")
)
