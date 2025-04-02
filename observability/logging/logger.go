package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LogLevel 定义日志级别
type LogLevel int

const (
	// 调试信息
	LevelDebug LogLevel = iota
	// 普通信息
	LevelInfo
	// 警告信息
	LevelWarn
	// 错误信息
	LevelError
	// 严重错误
	LevelFatal
)

// 日志级别名称
var levelNames = map[LogLevel]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
	LevelFatal: "FATAL",
}

// Logger 是结构化日志接口
type Logger interface {
	// 写入指定级别的日志
	Log(level LogLevel, msg string, fields map[string]interface{})

	// 调试日志
	Debug(msg string, fields map[string]interface{})
	// 普通信息
	Info(msg string, fields map[string]interface{})
	// 警告信息
	Warn(msg string, fields map[string]interface{})
	// 错误信息
	Error(msg string, fields map[string]interface{})
	// 严重错误
	Fatal(msg string, fields map[string]interface{})

	// 创建子日志
	With(fields map[string]interface{}) Logger

	// 设置日志级别
	SetLevel(level LogLevel)

	// 获取日志级别
	GetLevel() LogLevel
}

// 公共字段名
const (
	FieldTime     = "time"
	FieldLevel    = "level"
	FieldMessage  = "message"
	FieldFile     = "file"
	FieldLine     = "line"
	FieldService  = "service"
	FieldMethod   = "method"
	FieldDuration = "duration_ms"
	FieldStatus   = "status"
	FieldError    = "error"
	FieldTraceID  = "trace_id"
	FieldSpanID   = "span_id"
)

// Options 定义日志选项
type Options struct {
	// 输出位置
	Writer io.Writer
	// 最低日志级别
	Level LogLevel
	// 是否包含调用位置
	IncludeLocation bool
	// 服务名称
	ServiceName string
	// 是否使用彩色输出
	EnableColor bool
	// 是否格式化JSON输出
	PrettyPrint bool
}

// DefaultOptions 返回默认日志选项
func DefaultOptions() Options {
	return Options{
		Writer:          os.Stdout,
		Level:           LevelInfo,
		IncludeLocation: true,
		ServiceName:     "eidola",
		EnableColor:     true,
		PrettyPrint:     false,
	}
}

// StructuredLogger 实现结构化日志
type StructuredLogger struct {
	mu       sync.Mutex
	opts     Options
	fields   map[string]interface{}
	colorMap map[LogLevel]string
}

// NewStructuredLogger 创建新的结构化日志器
func NewStructuredLogger(opts Options) *StructuredLogger {
	// 创建颜色映射
	colorMap := map[LogLevel]string{
		LevelDebug: "\033[37m", // 灰色
		LevelInfo:  "\033[34m", // 蓝色
		LevelWarn:  "\033[33m", // 黄色
		LevelError: "\033[31m", // 红色
		LevelFatal: "\033[35m", // 紫色
	}

	return &StructuredLogger{
		opts:     opts,
		fields:   make(map[string]interface{}),
		colorMap: colorMap,
	}
}

// Log 记录指定级别的日志
func (l *StructuredLogger) Log(level LogLevel, msg string, fields map[string]interface{}) {
	// 如果级别低于设置的级别，忽略
	if level < l.opts.Level {
		return
	}

	// 创建日志条目
	entry := make(map[string]interface{})

	// 添加基本字段
	entry[FieldTime] = time.Now().Format(time.RFC3339)
	entry[FieldLevel] = levelNames[level]
	entry[FieldMessage] = msg

	// 添加服务名
	if l.opts.ServiceName != "" {
		entry["service_name"] = l.opts.ServiceName
	}

	// 如果需要添加调用位置
	if l.opts.IncludeLocation {
		_, file, line, ok := runtime.Caller(2)
		if ok {
			// 简化文件路径，只保留最后部分
			if idx := strings.LastIndex(file, "/"); idx >= 0 {
				file = file[idx+1:]
			}
			entry[FieldFile] = file
			entry[FieldLine] = line
		}
	}

	// 添加默认字段
	for k, v := range l.fields {
		entry[k] = v
	}

	// 添加临时字段
	for k, v := range fields {
		entry[k] = v
	}

	var output []byte
	var err error

	// 格式化JSON
	if l.opts.PrettyPrint {
		output, err = json.MarshalIndent(entry, "", "  ")
	} else {
		output, err = json.Marshal(entry)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling log entry: %v\n", err)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// 加入彩色输出
	if l.opts.EnableColor {
		colorCode, ok := l.colorMap[level]
		if ok {
			fmt.Fprintf(l.opts.Writer, "%s%s\033[0m\n", colorCode, output)
			return
		}
	}

	// 普通输出
	fmt.Fprintf(l.opts.Writer, "%s\n", output)

	// 如果是Fatal级别，退出程序
	if level == LevelFatal {
		os.Exit(1)
	}
}

// Debug 记录调试日志
func (l *StructuredLogger) Debug(msg string, fields map[string]interface{}) {
	l.Log(LevelDebug, msg, fields)
}

// Info 记录普通信息
func (l *StructuredLogger) Info(msg string, fields map[string]interface{}) {
	l.Log(LevelInfo, msg, fields)
}

// Warn 记录警告信息
func (l *StructuredLogger) Warn(msg string, fields map[string]interface{}) {
	l.Log(LevelWarn, msg, fields)
}

// Error 记录错误信息
func (l *StructuredLogger) Error(msg string, fields map[string]interface{}) {
	l.Log(LevelError, msg, fields)
}

// Fatal 记录致命错误并退出
func (l *StructuredLogger) Fatal(msg string, fields map[string]interface{}) {
	l.Log(LevelFatal, msg, fields)
}

// With 创建带有额外字段的子日志器
func (l *StructuredLogger) With(fields map[string]interface{}) Logger {
	// 创建新的日志器
	child := &StructuredLogger{
		opts:     l.opts,
		fields:   make(map[string]interface{}, len(l.fields)+len(fields)),
		colorMap: l.colorMap,
	}

	// 复制父日志器的字段
	for k, v := range l.fields {
		child.fields[k] = v
	}

	// 添加新字段
	for k, v := range fields {
		child.fields[k] = v
	}

	return child
}

// SetLevel 设置日志级别
func (l *StructuredLogger) SetLevel(level LogLevel) {
	l.opts.Level = level
}

// GetLevel 获取日志级别
func (l *StructuredLogger) GetLevel() LogLevel {
	return l.opts.Level
}

// GrpcUnaryServerInterceptor 返回一个用于记录gRPC一元调用的拦截器
func GrpcUnaryServerInterceptor(logger Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		// 提取方法信息
		fullMethodName := info.FullMethod
		service, method := splitMethodName(fullMethodName)

		// 记录请求开始
		startTime := time.Now()

		// 从上下文中提取跟踪ID
		traceID, spanID := extractTraceInfo(ctx)

		// 构建日志字段
		requestFields := map[string]interface{}{
			FieldService: service,
			FieldMethod:  method,
		}

		// 添加跟踪信息
		if traceID != "" {
			requestFields[FieldTraceID] = traceID
		}
		if spanID != "" {
			requestFields[FieldSpanID] = spanID
		}

		// 记录请求开始
		logger.Debug("gRPC服务请求开始", requestFields)

		// 处理请求
		resp, err = handler(ctx, req)

		// 计算耗时
		duration := time.Since(startTime)

		// 创建响应日志字段
		responseFields := map[string]interface{}{
			FieldService:  service,
			FieldMethod:   method,
			FieldDuration: float64(duration.Milliseconds()),
		}

		// 添加跟踪信息
		if traceID != "" {
			responseFields[FieldTraceID] = traceID
		}
		if spanID != "" {
			responseFields[FieldSpanID] = spanID
		}

		// 添加状态信息
		if err != nil {
			// 获取错误状态码
			st, ok := status.FromError(err)
			if ok {
				responseFields[FieldStatus] = st.Code().String()
			} else {
				responseFields[FieldStatus] = "UNKNOWN"
			}
			responseFields[FieldError] = err.Error()

			// 根据错误级别记录
			if st != nil && (st.Code() == codes.Internal || st.Code() == codes.Unknown || st.Code() == codes.DataLoss) {
				logger.Error("gRPC服务请求失败", responseFields)
			} else {
				logger.Warn("gRPC服务请求异常", responseFields)
			}
		} else {
			responseFields[FieldStatus] = codes.OK.String()
			logger.Info("gRPC服务请求完成", responseFields)
		}

		return resp, err
	}
}

// GrpcStreamServerInterceptor 返回一个用于记录gRPC流式调用的拦截器
func GrpcStreamServerInterceptor(logger Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 提取方法信息
		fullMethodName := info.FullMethod
		service, method := splitMethodName(fullMethodName)

		// 记录请求开始
		startTime := time.Now()

		// 从上下文中提取跟踪ID
		traceID, spanID := extractTraceInfo(ss.Context())

		// 构建日志字段
		requestFields := map[string]interface{}{
			FieldService: service,
			FieldMethod:  method,
		}

		// 添加流类型
		if info.IsClientStream {
			requestFields["client_stream"] = true
		}
		if info.IsServerStream {
			requestFields["server_stream"] = true
		}

		// 添加跟踪信息
		if traceID != "" {
			requestFields[FieldTraceID] = traceID
		}
		if spanID != "" {
			requestFields[FieldSpanID] = spanID
		}

		// 记录流开始
		logger.Debug("gRPC流式服务开始", requestFields)

		// 处理流
		err := handler(srv, &monitoredServerStream{
			ServerStream: ss,
			logger:       logger,
			service:      service,
			method:       method,
			traceID:      traceID,
			spanID:       spanID,
		})

		// 计算耗时
		duration := time.Since(startTime)

		// 创建响应日志字段
		responseFields := map[string]interface{}{
			FieldService:  service,
			FieldMethod:   method,
			FieldDuration: float64(duration.Milliseconds()),
		}

		// 添加跟踪信息
		if traceID != "" {
			responseFields[FieldTraceID] = traceID
		}
		if spanID != "" {
			responseFields[FieldSpanID] = spanID
		}

		// 添加状态信息
		if err != nil {
			// 获取错误状态码
			st, ok := status.FromError(err)
			if ok {
				responseFields[FieldStatus] = st.Code().String()
			} else {
				responseFields[FieldStatus] = "UNKNOWN"
			}
			responseFields[FieldError] = err.Error()

			// 根据错误级别记录
			if st != nil && (st.Code() == codes.Internal || st.Code() == codes.Unknown || st.Code() == codes.DataLoss) {
				logger.Error("gRPC流式服务失败", responseFields)
			} else {
				logger.Warn("gRPC流式服务异常", responseFields)
			}
		} else {
			responseFields[FieldStatus] = codes.OK.String()
			logger.Info("gRPC流式服务完成", responseFields)
		}

		return err
	}
}

// monitoredServerStream 是一个包装的gRPC流，用于记录流操作
type monitoredServerStream struct {
	grpc.ServerStream
	logger    Logger
	service   string
	method    string
	traceID   string
	spanID    string
	recvCount int
	sendCount int
}

func (s *monitoredServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)

	if err == nil {
		s.recvCount++
		if s.recvCount%100 == 0 { // 每收到100条消息记录一次，避免日志过多
			s.logger.Debug("接收流消息", map[string]interface{}{
				FieldService: s.service,
				FieldMethod:  s.method,
				"count":      s.recvCount,
				FieldTraceID: s.traceID,
				FieldSpanID:  s.spanID,
			})
		}
	}

	return err
}

func (s *monitoredServerStream) SendMsg(m interface{}) error {
	err := s.ServerStream.SendMsg(m)

	if err == nil {
		s.sendCount++
		if s.sendCount%100 == 0 { // 每发送100条消息记录一次，避免日志过多
			s.logger.Debug("发送流消息", map[string]interface{}{
				FieldService: s.service,
				FieldMethod:  s.method,
				"count":      s.sendCount,
				FieldTraceID: s.traceID,
				FieldSpanID:  s.spanID,
			})
		}
	}

	return err
}

// GrpcUnaryClientInterceptor 返回一个用于记录gRPC客户端一元调用的拦截器
func GrpcUnaryClientInterceptor(logger Logger) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 提取方法信息
		service, methodName := splitMethodName(method)

		// 记录请求开始
		startTime := time.Now()

		// 从上下文中提取跟踪ID
		traceID, spanID := extractTraceInfo(ctx)

		// 构建日志字段
		requestFields := map[string]interface{}{
			FieldService: service,
			FieldMethod:  methodName,
			"target":     cc.Target(),
		}

		// 添加跟踪信息
		if traceID != "" {
			requestFields[FieldTraceID] = traceID
		}
		if spanID != "" {
			requestFields[FieldSpanID] = spanID
		}

		// 记录请求开始
		logger.Debug("gRPC客户端请求开始", requestFields)

		// 处理请求
		err := invoker(ctx, method, req, reply, cc, opts...)

		// 计算耗时
		duration := time.Since(startTime)

		// 创建响应日志字段
		responseFields := map[string]interface{}{
			FieldService:  service,
			FieldMethod:   methodName,
			FieldDuration: float64(duration.Milliseconds()),
			"target":      cc.Target(),
		}

		// 添加跟踪信息
		if traceID != "" {
			responseFields[FieldTraceID] = traceID
		}
		if spanID != "" {
			responseFields[FieldSpanID] = spanID
		}

		// 添加状态信息
		if err != nil {
			// 获取错误状态码
			st, ok := status.FromError(err)
			if ok {
				responseFields[FieldStatus] = st.Code().String()
			} else {
				responseFields[FieldStatus] = "UNKNOWN"
			}
			responseFields[FieldError] = err.Error()

			// 根据错误级别记录
			if st != nil && (st.Code() == codes.Internal || st.Code() == codes.Unknown || st.Code() == codes.DataLoss) {
				logger.Error("gRPC客户端请求失败", responseFields)
			} else {
				logger.Warn("gRPC客户端请求异常", responseFields)
			}
		} else {
			responseFields[FieldStatus] = codes.OK.String()
			logger.Info("gRPC客户端请求完成", responseFields)
		}

		return err
	}
}

// 提取跟踪信息 - 实际实现应从上下文中提取OpenTelemetry跟踪信息
func extractTraceInfo(ctx context.Context) (string, string) {
	// 这里是一个简化的实现，实际应用中应该从OpenTelemetry上下文中提取
	// 此处仅作为占位实现
	return "", ""
}

// 解析完整方法名为服务名和方法名
// 输入格式: /package.service/method
func splitMethodName(fullMethodName string) (string, string) {
	fullMethodName = fullMethodName[1:] // 去掉前导 "/"

	// 查找最后一个 "/"
	pos := 0
	for i := 0; i < len(fullMethodName); i++ {
		if fullMethodName[i] == '/' {
			pos = i
		}
	}

	service := fullMethodName[:pos]
	method := fullMethodName[pos+1:]

	return service, method
}

// 全局默认日志器
var defaultLogger Logger

// 初始化全局默认日志器
func init() {
	defaultLogger = NewStructuredLogger(DefaultOptions())
}

// 获取全局默认日志器
func GetDefaultLogger() Logger {
	return defaultLogger
}

// 设置全局默认日志器
func SetDefaultLogger(logger Logger) {
	defaultLogger = logger
}

// GetFileLogger 创建一个输出到文件的日志器
func GetFileLogger(filename string, level LogLevel) (Logger, error) {
	// 创建日志文件
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("无法创建日志文件: %v", err)
	}

	// 创建日志器选项
	opts := DefaultOptions()
	opts.Writer = file
	opts.Level = level
	opts.EnableColor = false // 文件日志不需要颜色

	return NewStructuredLogger(opts), nil
}
