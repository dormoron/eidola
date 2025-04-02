package errs

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dormoron/eidola/observability/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorSeverity 定义错误的严重程度
type ErrorSeverity int

const (
	// SeverityInfo 表示信息性错误，不影响系统运行
	SeverityInfo ErrorSeverity = iota
	// SeverityWarning 表示警告，系统可以继续运行但可能存在问题
	SeverityWarning
	// SeverityError 表示错误，请求可能无法完成但系统仍然可以运行
	SeverityError
	// SeverityCritical 表示严重错误，系统功能可能受到影响
	SeverityCritical
	// SeverityFatal 表示致命错误，系统无法继续运行
	SeverityFatal
)

// String 返回错误严重程度的字符串表示
func (s ErrorSeverity) String() string {
	switch s {
	case SeverityInfo:
		return "INFO"
	case SeverityWarning:
		return "WARNING"
	case SeverityError:
		return "ERROR"
	case SeverityCritical:
		return "CRITICAL"
	case SeverityFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ErrorCategory 定义错误的类别
type ErrorCategory string

const (
	// CategoryNetwork 网络相关错误
	CategoryNetwork ErrorCategory = "NETWORK"
	// CategoryDatabase 数据库相关错误
	CategoryDatabase ErrorCategory = "DATABASE"
	// CategoryValidation 验证相关错误
	CategoryValidation ErrorCategory = "VALIDATION"
	// CategoryAuth 认证授权相关错误
	CategoryAuth ErrorCategory = "AUTH"
	// CategoryBusiness 业务逻辑相关错误
	CategoryBusiness ErrorCategory = "BUSINESS"
	// CategorySystem 系统相关错误
	CategorySystem ErrorCategory = "SYSTEM"
	// CategoryExternal 外部系统相关错误
	CategoryExternal ErrorCategory = "EXTERNAL"
	// CategoryUnknown 未知类别错误
	CategoryUnknown ErrorCategory = "UNKNOWN"
)

// ErrorRecoveryStrategy 定义错误的恢复策略
type ErrorRecoveryStrategy int

const (
	// RecoveryRetry 重试策略
	RecoveryRetry ErrorRecoveryStrategy = iota
	// RecoveryFallback 降级策略
	RecoveryFallback
	// RecoveryCircuitBreak 熔断策略
	RecoveryCircuitBreak
	// RecoveryIgnore 忽略策略
	RecoveryIgnore
	// RecoveryAbort 终止策略
	RecoveryAbort
)

// String 返回错误恢复策略的字符串表示
func (s ErrorRecoveryStrategy) String() string {
	switch s {
	case RecoveryRetry:
		return "RETRY"
	case RecoveryFallback:
		return "FALLBACK"
	case RecoveryCircuitBreak:
		return "CIRCUIT_BREAK"
	case RecoveryIgnore:
		return "IGNORE"
	case RecoveryAbort:
		return "ABORT"
	default:
		return "UNKNOWN"
	}
}

// EnhancedError 增强错误接口
type EnhancedError interface {
	error
	// ID 返回错误的唯一标识符
	ID() string
	// Code 返回错误代码
	Code() string
	// GRPCStatus 将错误转换为gRPC状态
	GRPCStatus() *status.Status
	// Severity 返回错误的严重程度
	Severity() ErrorSeverity
	// Category 返回错误的类别
	Category() ErrorCategory
	// Metadata 返回错误的元数据
	Metadata() map[string]interface{}
	// RecoveryStrategy 返回错误的恢复策略
	RecoveryStrategy() ErrorRecoveryStrategy
	// Trace 返回错误的调用栈信息
	Trace() []string
	// Timeout 返回错误是否由超时引起
	Timeout() bool
	// Temporary 返回错误是否是临时性的
	Temporary() bool
	// Cause 返回错误的根因
	Cause() error
	// WithMetadata 添加元数据到错误
	WithMetadata(key string, value interface{}) EnhancedError
	// WithRecoveryStrategy 设置错误的恢复策略
	WithRecoveryStrategy(strategy ErrorRecoveryStrategy) EnhancedError
	// MarshalJSON 实现JSON序列化
	MarshalJSON() ([]byte, error)
}

// enhancedError 增强错误的默认实现
type enhancedError struct {
	id               string
	code             string
	message          string
	grpcCode         codes.Code
	severity         ErrorSeverity
	category         ErrorCategory
	metadata         map[string]interface{}
	recoveryStrategy ErrorRecoveryStrategy
	trace            []string
	isTimeout        bool
	isTemporary      bool
	cause            error
	timestamp        time.Time
	callerInfo       string
}

// frameInfo 存储调用栈帧信息
type frameInfo struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
}

// NewEnhancedError 创建新的增强错误
func NewEnhancedError(code string, message string, severity ErrorSeverity, category ErrorCategory) EnhancedError {
	err := &enhancedError{
		id:               generateErrorID(),
		code:             code,
		message:          message,
		grpcCode:         mapSeverityToGRPCCode(severity),
		severity:         severity,
		category:         category,
		metadata:         make(map[string]interface{}),
		recoveryStrategy: chooseDefaultRecoveryStrategy(severity),
		trace:            captureStackTrace(2), // 跳过本函数和调用者
		timestamp:        time.Now(),
		callerInfo:       caller(2), // 跳过本函数和调用者
	}
	return err
}

// FromError 从已有错误创建增强错误
func FromError(err error, code string, severity ErrorSeverity, category ErrorCategory) EnhancedError {
	if err == nil {
		return nil
	}

	// 检查是否已经是增强错误
	if enhancedErr, ok := err.(EnhancedError); ok {
		return enhancedErr
	}

	// 检查是否是 gRPC 状态错误
	if st, ok := status.FromError(err); ok {
		return &enhancedError{
			id:               generateErrorID(),
			code:             code,
			message:          st.Message(),
			grpcCode:         st.Code(),
			severity:         severity,
			category:         category,
			metadata:         make(map[string]interface{}),
			recoveryStrategy: chooseDefaultRecoveryStrategy(severity),
			trace:            captureStackTrace(2),
			timestamp:        time.Now(),
			callerInfo:       caller(2),
			cause:            err,
		}
	}

	return &enhancedError{
		id:               generateErrorID(),
		code:             code,
		message:          err.Error(),
		grpcCode:         mapSeverityToGRPCCode(severity),
		severity:         severity,
		category:         category,
		metadata:         make(map[string]interface{}),
		recoveryStrategy: chooseDefaultRecoveryStrategy(severity),
		trace:            captureStackTrace(2),
		timestamp:        time.Now(),
		callerInfo:       caller(2),
		cause:            err,
	}
}

// Error 实现error接口
func (e *enhancedError) Error() string {
	return fmt.Sprintf("[%s][%s] %s: %s", e.category, e.severity, e.code, e.message)
}

// ID 返回错误的唯一标识符
func (e *enhancedError) ID() string {
	return e.id
}

// Code 返回错误代码
func (e *enhancedError) Code() string {
	return e.code
}

// GRPCStatus 将错误转换为gRPC状态
func (e *enhancedError) GRPCStatus() *status.Status {
	// 创建包含元数据的状态
	return status.New(e.grpcCode, e.message)
}

// Severity 返回错误的严重程度
func (e *enhancedError) Severity() ErrorSeverity {
	return e.severity
}

// Category 返回错误的类别
func (e *enhancedError) Category() ErrorCategory {
	return e.category
}

// Metadata 返回错误的元数据
func (e *enhancedError) Metadata() map[string]interface{} {
	return e.metadata
}

// RecoveryStrategy 返回错误的恢复策略
func (e *enhancedError) RecoveryStrategy() ErrorRecoveryStrategy {
	return e.recoveryStrategy
}

// Trace 返回错误的调用栈信息
func (e *enhancedError) Trace() []string {
	return e.trace
}

// Timeout 返回错误是否由超时引起
func (e *enhancedError) Timeout() bool {
	return e.isTimeout
}

// Temporary 返回错误是否是临时性的
func (e *enhancedError) Temporary() bool {
	return e.isTemporary
}

// Cause 返回错误的根因
func (e *enhancedError) Cause() error {
	return e.cause
}

// WithMetadata 添加元数据到错误
func (e *enhancedError) WithMetadata(key string, value interface{}) EnhancedError {
	e.metadata[key] = value
	return e
}

// WithRecoveryStrategy 设置错误的恢复策略
func (e *enhancedError) WithRecoveryStrategy(strategy ErrorRecoveryStrategy) EnhancedError {
	e.recoveryStrategy = strategy
	return e
}

// MarshalJSON 实现JSON序列化
func (e *enhancedError) MarshalJSON() ([]byte, error) {
	var causeStr string
	if e.cause != nil {
		causeStr = e.cause.Error()
	}

	type jsonError struct {
		ID               string                 `json:"id"`
		Code             string                 `json:"code"`
		Message          string                 `json:"message"`
		Severity         string                 `json:"severity"`
		Category         string                 `json:"category"`
		RecoveryStrategy string                 `json:"recovery_strategy"`
		Trace            []string               `json:"trace,omitempty"`
		IsTimeout        bool                   `json:"is_timeout"`
		IsTemporary      bool                   `json:"is_temporary"`
		Cause            string                 `json:"cause,omitempty"`
		Timestamp        string                 `json:"timestamp"`
		CallerInfo       string                 `json:"caller_info"`
		Metadata         map[string]interface{} `json:"metadata,omitempty"`
	}

	return json.Marshal(jsonError{
		ID:               e.id,
		Code:             e.code,
		Message:          e.message,
		Severity:         e.severity.String(),
		Category:         string(e.category),
		RecoveryStrategy: e.recoveryStrategy.String(),
		Trace:            e.trace,
		IsTimeout:        e.isTimeout,
		IsTemporary:      e.isTemporary,
		Cause:            causeStr,
		Timestamp:        e.timestamp.Format(time.RFC3339),
		CallerInfo:       e.callerInfo,
		Metadata:         e.metadata,
	})
}

// 错误聚合与分析

// ErrorAggregator 错误聚合器，收集并分析错误
type ErrorAggregator struct {
	errors      map[string]*aggregatedError // 按错误代码聚合
	maxCapacity int
	mu          sync.RWMutex
	logger      logging.Logger
}

// 聚合的错误信息
type aggregatedError struct {
	Code       string
	Count      int
	FirstSeen  time.Time
	LastSeen   time.Time
	Samples    []EnhancedError
	MaxSamples int
}

// NewErrorAggregator 创建新的错误聚合器
func NewErrorAggregator(capacity int, logger logging.Logger) *ErrorAggregator {
	return &ErrorAggregator{
		errors:      make(map[string]*aggregatedError),
		maxCapacity: capacity,
		logger:      logger,
	}
}

// Record 记录一个错误
func (ea *ErrorAggregator) Record(err EnhancedError) {
	if err == nil {
		return
	}

	ea.mu.Lock()
	defer ea.mu.Unlock()

	code := err.Code()
	if aggregated, exists := ea.errors[code]; exists {
		// 已存在的错误代码，更新计数和时间戳
		aggregated.Count++
		aggregated.LastSeen = time.Now()

		// 保留一定数量的样本
		if len(aggregated.Samples) < aggregated.MaxSamples {
			aggregated.Samples = append(aggregated.Samples, err)
		}
	} else {
		// 新的错误代码
		ea.errors[code] = &aggregatedError{
			Code:       code,
			Count:      1,
			FirstSeen:  time.Now(),
			LastSeen:   time.Now(),
			Samples:    []EnhancedError{err},
			MaxSamples: 10, // 默认保留10个样本
		}
	}

	// 记录严重错误
	if err.Severity() >= SeverityError {
		ea.logger.Error("严重错误", map[string]interface{}{
			"error_id":      err.ID(),
			"error_code":    err.Code(),
			"error_message": err.Error(),
			"severity":      err.Severity().String(),
			"category":      err.Category(),
			"caller":        err.Trace()[0],
		})
	}
}

// GetAggregatedErrors 获取聚合的错误信息
func (ea *ErrorAggregator) GetAggregatedErrors() map[string]*aggregatedError {
	ea.mu.RLock()
	defer ea.mu.RUnlock()

	// 返回副本以避免并发问题
	result := make(map[string]*aggregatedError, len(ea.errors))
	for k, v := range ea.errors {
		result[k] = v
	}
	return result
}

// Clear 清除所有聚合的错误
func (ea *ErrorAggregator) Clear() {
	ea.mu.Lock()
	defer ea.mu.Unlock()

	ea.errors = make(map[string]*aggregatedError)
}

// GetTopErrors 获取出现频率最高的错误
func (ea *ErrorAggregator) GetTopErrors(n int) []*aggregatedError {
	ea.mu.RLock()
	defer ea.mu.RUnlock()

	// 将错误转换为切片以便排序
	errorSlice := make([]*aggregatedError, 0, len(ea.errors))
	for _, v := range ea.errors {
		errorSlice = append(errorSlice, v)
	}

	// 按计数排序
	// 简单排序实现
	for i := 0; i < len(errorSlice); i++ {
		for j := i + 1; j < len(errorSlice); j++ {
			if errorSlice[i].Count < errorSlice[j].Count {
				errorSlice[i], errorSlice[j] = errorSlice[j], errorSlice[i]
			}
		}
	}

	// 返回前n个
	if n > len(errorSlice) {
		n = len(errorSlice)
	}
	return errorSlice[:n]
}

// GetErrorsBySeverity 按严重程度获取错误
func (ea *ErrorAggregator) GetErrorsBySeverity(severity ErrorSeverity) []*aggregatedError {
	ea.mu.RLock()
	defer ea.mu.RUnlock()

	var result []*aggregatedError
	for _, err := range ea.errors {
		// 检查样本中的第一个错误的严重程度
		if len(err.Samples) > 0 && err.Samples[0].Severity() == severity {
			result = append(result, err)
		}
	}
	return result
}

// GetErrorsByCategory 按类别获取错误
func (ea *ErrorAggregator) GetErrorsByCategory(category ErrorCategory) []*aggregatedError {
	ea.mu.RLock()
	defer ea.mu.RUnlock()

	var result []*aggregatedError
	for _, err := range ea.errors {
		// 检查样本中的第一个错误的类别
		if len(err.Samples) > 0 && err.Samples[0].Category() == category {
			result = append(result, err)
		}
	}
	return result
}

// 辅助函数

// generateErrorID 生成唯一的错误ID
func generateErrorID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), randomInt())
}

// 生成随机整数
func randomInt() int64 {
	return time.Now().UnixNano() % 1000000
}

// captureStackTrace 捕获调用栈信息
func captureStackTrace(skip int) []string {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(skip, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	var trace []string
	for {
		frame, more := frames.Next()
		// 跳过标准库和运行时函数
		if !strings.Contains(frame.File, "runtime/") {
			file := filepath.Base(frame.File)
			trace = append(trace, fmt.Sprintf("%s:%d %s", file, frame.Line, frame.Function))
		}
		if !more {
			break
		}
	}
	return trace
}

// caller 获取调用者信息
func caller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}

// mapSeverityToGRPCCode 将错误严重程度映射到gRPC状态码
func mapSeverityToGRPCCode(severity ErrorSeverity) codes.Code {
	switch severity {
	case SeverityInfo:
		return codes.OK
	case SeverityWarning:
		return codes.Internal
	case SeverityError:
		return codes.Internal
	case SeverityCritical:
		return codes.Internal
	case SeverityFatal:
		return codes.Internal
	default:
		return codes.Unknown
	}
}

// chooseDefaultRecoveryStrategy 根据错误严重程度选择默认恢复策略
func chooseDefaultRecoveryStrategy(severity ErrorSeverity) ErrorRecoveryStrategy {
	switch severity {
	case SeverityInfo:
		return RecoveryIgnore
	case SeverityWarning:
		return RecoveryRetry
	case SeverityError:
		return RecoveryFallback
	case SeverityCritical:
		return RecoveryCircuitBreak
	case SeverityFatal:
		return RecoveryAbort
	default:
		return RecoveryRetry
	}
}

// 全局错误聚合器实例
var (
	globalErrorAggregator   *ErrorAggregator
	initErrorAggregatorOnce sync.Once
)

// GetGlobalErrorAggregator 获取全局错误聚合器
func GetGlobalErrorAggregator() *ErrorAggregator {
	initErrorAggregatorOnce.Do(func() {
		globalErrorAggregator = NewErrorAggregator(1000, nil)
	})
	return globalErrorAggregator
}
