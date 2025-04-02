package security

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// 服务隔离错误定义
var (
	ErrServiceNotAllowed     = errors.New("服务调用不被允许")
	ErrRateLimitExceeded     = errors.New("超过服务调用速率限制")
	ErrServiceUnavailable    = errors.New("目标服务不可用")
	ErrMethodNotAllowed      = errors.New("方法调用不被允许")
	ErrInvalidSegregationKey = errors.New("无效的隔离键")
)

// ServiceIdentity 表示服务身份
type ServiceIdentity struct {
	Name      string            // 服务名称
	Namespace string            // 命名空间
	Version   string            // 版本
	Roles     []string          // 服务角色
	Metadata  map[string]string // 元数据
}

// String 返回服务标识的字符串表示
func (s ServiceIdentity) String() string {
	return fmt.Sprintf("%s.%s-v%s", s.Namespace, s.Name, s.Version)
}

// ServiceDecorator 定义了服务之间的通信装饰器
type ServiceDecorator interface {
	// Decorate 对服务间调用进行装饰
	Decorate(ctx context.Context, target ServiceIdentity, method string) (context.Context, error)
	// Name 返回装饰器的名称
	Name() string
}

// AccessControl 是一个控制服务间访问的接口
type AccessControl interface {
	// AllowCall 决定是否允许服务调用
	AllowCall(from, to ServiceIdentity, method string) bool
	// RecordCall 记录服务调用
	RecordCall(from, to ServiceIdentity, method string, timestamp time.Time, duration time.Duration, err error)
	// GetAllowed 获取允许调用的服务列表
	GetAllowed(from ServiceIdentity) []ServiceIdentity
}

// ServiceIsolator 实现了服务隔离
type ServiceIsolator struct {
	accessControl   AccessControl
	decorators      []ServiceDecorator
	services        map[string]ServiceIdentity
	segregationKeys map[string]string // 隔离键 -> 隔离组
	mu              sync.RWMutex
}

// ServiceIsolatorOption 是ServiceIsolator的选项函数
type ServiceIsolatorOption func(*ServiceIsolator)

// WithAccessControl 设置访问控制组件
func WithAccessControl(ac AccessControl) ServiceIsolatorOption {
	return func(si *ServiceIsolator) {
		si.accessControl = ac
	}
}

// WithDecorators 设置服务装饰器
func WithDecorators(decorators ...ServiceDecorator) ServiceIsolatorOption {
	return func(si *ServiceIsolator) {
		si.decorators = append(si.decorators, decorators...)
	}
}

// NewServiceIsolator 创建新的服务隔离器
func NewServiceIsolator(opts ...ServiceIsolatorOption) *ServiceIsolator {
	isolator := &ServiceIsolator{
		decorators:      make([]ServiceDecorator, 0),
		services:        make(map[string]ServiceIdentity),
		segregationKeys: make(map[string]string),
	}

	// 应用选项
	for _, opt := range opts {
		opt(isolator)
	}

	// 如果没有设置访问控制，使用默认的
	if isolator.accessControl == nil {
		isolator.accessControl = NewDefaultAccessControl()
	}

	return isolator
}

// RegisterService 注册服务
func (si *ServiceIsolator) RegisterService(service ServiceIdentity) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.services[service.String()] = service
}

// DeregisterService 注销服务
func (si *ServiceIsolator) DeregisterService(service ServiceIdentity) {
	si.mu.Lock()
	defer si.mu.Unlock()
	delete(si.services, service.String())
}

// GetService 获取服务身份
func (si *ServiceIsolator) GetService(serviceName string) (ServiceIdentity, bool) {
	si.mu.RLock()
	defer si.mu.RUnlock()

	// 直接匹配
	if service, ok := si.services[serviceName]; ok {
		return service, true
	}

	// 部分匹配
	for name, service := range si.services {
		if strings.Contains(name, serviceName) {
			return service, true
		}
	}

	return ServiceIdentity{}, false
}

// RegisterSegregationKey 注册隔离键，用于服务分组
func (si *ServiceIsolator) RegisterSegregationKey(key, group string) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.segregationKeys[key] = group
}

// RemoveSegregationKey 移除隔离键
func (si *ServiceIsolator) RemoveSegregationKey(key string) {
	si.mu.Lock()
	defer si.mu.Unlock()
	delete(si.segregationKeys, key)
}

// GetSegregationGroup 获取隔离组
func (si *ServiceIsolator) GetSegregationGroup(key string) (string, error) {
	si.mu.RLock()
	defer si.mu.RUnlock()
	group, ok := si.segregationKeys[key]
	if !ok {
		return "", ErrInvalidSegregationKey
	}
	return group, nil
}

// AddDecorator 添加服务装饰器
func (si *ServiceIsolator) AddDecorator(decorator ServiceDecorator) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.decorators = append(si.decorators, decorator)
}

// PrepareCall 准备服务调用
func (si *ServiceIsolator) PrepareCall(ctx context.Context, from, to ServiceIdentity, method string) (context.Context, error) {
	// 检查访问权限
	if si.accessControl != nil && !si.accessControl.AllowCall(from, to, method) {
		return ctx, ErrServiceNotAllowed
	}

	// 应用所有装饰器
	var err error
	for _, decorator := range si.decorators {
		ctx, err = decorator.Decorate(ctx, to, method)
		if err != nil {
			return ctx, err
		}
	}

	return ctx, nil
}

// RecordCall 记录服务调用
func (si *ServiceIsolator) RecordCall(from, to ServiceIdentity, method string, timestamp time.Time, duration time.Duration, err error) {
	if si.accessControl != nil {
		si.accessControl.RecordCall(from, to, method, timestamp, duration, err)
	}
}

// GetAllowedServices 获取允许调用的服务列表
func (si *ServiceIsolator) GetAllowedServices(from ServiceIdentity) []ServiceIdentity {
	if si.accessControl != nil {
		return si.accessControl.GetAllowed(from)
	}
	return []ServiceIdentity{}
}

// ================ 默认访问控制实现 ================

// DefaultAccessControl 是默认的访问控制实现
type DefaultAccessControl struct {
	rules       map[string]map[string][]string // from -> to -> methods
	callRecords []CallRecord
	maxRecords  int
	mu          sync.RWMutex
}

// CallRecord 表示调用记录
type CallRecord struct {
	From      ServiceIdentity
	To        ServiceIdentity
	Method    string
	Timestamp time.Time
	Duration  time.Duration
	Error     error
}

// NewDefaultAccessControl 创建默认访问控制
func NewDefaultAccessControl() *DefaultAccessControl {
	return &DefaultAccessControl{
		rules:      make(map[string]map[string][]string),
		maxRecords: 1000, // 默认保留最近1000条记录
	}
}

// AddRule 添加访问规则
func (ac *DefaultAccessControl) AddRule(from, to ServiceIdentity, methods []string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	fromKey := from.String()
	toKey := to.String()

	// 初始化from映射（如果需要）
	if _, ok := ac.rules[fromKey]; !ok {
		ac.rules[fromKey] = make(map[string][]string)
	}

	// 添加方法列表
	ac.rules[fromKey][toKey] = methods
}

// RemoveRule 移除访问规则
func (ac *DefaultAccessControl) RemoveRule(from, to ServiceIdentity) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	fromKey := from.String()
	toKey := to.String()

	if toRules, ok := ac.rules[fromKey]; ok {
		delete(toRules, toKey)
		// 如果没有目标规则了，删除整个源
		if len(toRules) == 0 {
			delete(ac.rules, fromKey)
		}
	}
}

// AllowCall 实现AccessControl接口
func (ac *DefaultAccessControl) AllowCall(from, to ServiceIdentity, method string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	fromKey := from.String()
	toKey := to.String()

	// 检查是否有任何规则
	toRules, ok := ac.rules[fromKey]
	if !ok {
		return false
	}

	// 检查是否允许访问目标服务
	methods, ok := toRules[toKey]
	if !ok {
		return false
	}

	// 如果方法列表为空，允许所有方法
	if len(methods) == 0 {
		return true
	}

	// 检查是否允许特定方法
	for _, m := range methods {
		if m == "*" || m == method {
			return true
		}
		// 支持通配符前缀匹配
		if strings.HasSuffix(m, "*") && strings.HasPrefix(method, strings.TrimSuffix(m, "*")) {
			return true
		}
	}

	return false
}

// RecordCall 实现AccessControl接口
func (ac *DefaultAccessControl) RecordCall(from, to ServiceIdentity, method string, timestamp time.Time, duration time.Duration, err error) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	// 添加记录
	record := CallRecord{
		From:      from,
		To:        to,
		Method:    method,
		Timestamp: timestamp,
		Duration:  duration,
		Error:     err,
	}

	// 限制记录数量
	if len(ac.callRecords) >= ac.maxRecords {
		// 移除最老的记录
		ac.callRecords = ac.callRecords[1:]
	}

	ac.callRecords = append(ac.callRecords, record)
}

// GetRecords 获取所有调用记录
func (ac *DefaultAccessControl) GetRecords() []CallRecord {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	records := make([]CallRecord, len(ac.callRecords))
	copy(records, ac.callRecords)
	return records
}

// GetAllowed 实现AccessControl接口
func (ac *DefaultAccessControl) GetAllowed(from ServiceIdentity) []ServiceIdentity {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	fromKey := from.String()
	toRules, ok := ac.rules[fromKey]
	if !ok {
		return []ServiceIdentity{}
	}

	result := make([]ServiceIdentity, 0, len(toRules))
	for toKey := range toRules {
		// 这里简化处理，实际应该从注册的服务中查找
		parts := strings.Split(toKey, ".")
		if len(parts) >= 2 {
			versionParts := strings.Split(parts[1], "-v")
			version := ""
			name := parts[1]
			if len(versionParts) >= 2 {
				name = versionParts[0]
				version = versionParts[1]
			}

			service := ServiceIdentity{
				Namespace: parts[0],
				Name:      name,
				Version:   version,
			}
			result = append(result, service)
		}
	}

	return result
}

// SetMaxRecords 设置最大记录数
func (ac *DefaultAccessControl) SetMaxRecords(max int) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.maxRecords = max

	// 如果当前记录超过新的最大值，截断
	if len(ac.callRecords) > max {
		ac.callRecords = ac.callRecords[len(ac.callRecords)-max:]
	}
}

// ================ 速率限制装饰器 ================

// RateLimiter 表示速率限制器
type RateLimiter interface {
	// Allow 判断是否允许请求
	Allow(key string) bool
	// Record 记录一次请求
	Record(key string)
}

// RateLimitDecorator 实现了速率限制装饰器
type RateLimitDecorator struct {
	limiter RateLimiter
}

// NewRateLimitDecorator 创建速率限制装饰器
func NewRateLimitDecorator(limiter RateLimiter) *RateLimitDecorator {
	return &RateLimitDecorator{
		limiter: limiter,
	}
}

// Name 实现ServiceDecorator接口
func (d *RateLimitDecorator) Name() string {
	return "RateLimitDecorator"
}

// Decorate 实现ServiceDecorator接口
func (d *RateLimitDecorator) Decorate(ctx context.Context, target ServiceIdentity, method string) (context.Context, error) {
	key := fmt.Sprintf("%s-%s", target.String(), method)
	if !d.limiter.Allow(key) {
		return ctx, ErrRateLimitExceeded
	}
	d.limiter.Record(key)
	return ctx, nil
}

// ================ 隔离组装饰器 ================

// SegregationDecorator 实现了服务隔离组装饰器
type SegregationDecorator struct {
	isolator *ServiceIsolator
}

// NewSegregationDecorator 创建服务隔离组装饰器
func NewSegregationDecorator(isolator *ServiceIsolator) *SegregationDecorator {
	return &SegregationDecorator{
		isolator: isolator,
	}
}

// Name 实现ServiceDecorator接口
func (d *SegregationDecorator) Name() string {
	return "SegregationDecorator"
}

// Decorate 实现ServiceDecorator接口
func (d *SegregationDecorator) Decorate(ctx context.Context, target ServiceIdentity, method string) (context.Context, error) {
	// 从上下文中获取隔离键
	key, ok := ctx.Value("segregation_key").(string)
	if !ok {
		// 没有隔离键，允许调用
		return ctx, nil
	}

	// 获取目标服务的隔离组
	targetGroup, err := d.isolator.GetSegregationGroup(key)
	if err != nil {
		// 对于无法识别的隔离键，阻止调用
		return ctx, err
	}

	// 检查目标服务是否在同一隔离组
	if targetMeta, ok := target.Metadata["segregation_group"]; ok {
		if targetMeta != targetGroup {
			return ctx, ErrServiceNotAllowed
		}
	}

	return ctx, nil
}

// ================ 熔断装饰器 ================

// CircuitBreakerState 定义了熔断器状态
type CircuitBreakerState int

const (
	CircuitClosed   CircuitBreakerState = iota // 关闭状态（允许请求）
	CircuitOpen                                // 开启状态（阻止请求）
	CircuitHalfOpen                            // 半开状态（允许部分请求）
)

// CircuitBreaker 表示服务熔断器
type CircuitBreaker interface {
	// AllowRequest 判断是否允许请求
	AllowRequest(key string) bool
	// RecordSuccess 记录成功的请求
	RecordSuccess(key string)
	// RecordFailure 记录失败的请求
	RecordFailure(key string, err error)
	// GetState 获取熔断器状态
	GetState(key string) CircuitBreakerState
}

// CircuitBreakerDecorator 实现了熔断装饰器
type CircuitBreakerDecorator struct {
	breaker CircuitBreaker
}

// NewCircuitBreakerDecorator 创建熔断装饰器
func NewCircuitBreakerDecorator(breaker CircuitBreaker) *CircuitBreakerDecorator {
	return &CircuitBreakerDecorator{
		breaker: breaker,
	}
}

// Name 实现ServiceDecorator接口
func (d *CircuitBreakerDecorator) Name() string {
	return "CircuitBreakerDecorator"
}

// Decorate 实现ServiceDecorator接口
func (d *CircuitBreakerDecorator) Decorate(ctx context.Context, target ServiceIdentity, method string) (context.Context, error) {
	key := fmt.Sprintf("%s-%s", target.String(), method)
	if !d.breaker.AllowRequest(key) {
		return ctx, ErrServiceUnavailable
	}

	// 将熔断器通过上下文传递，以便后续记录结果
	return context.WithValue(ctx, "circuit_breaker_key", key), nil
}

// RecordResult 记录请求结果
func (d *CircuitBreakerDecorator) RecordResult(ctx context.Context, err error) {
	if key, ok := ctx.Value("circuit_breaker_key").(string); ok {
		if err == nil {
			d.breaker.RecordSuccess(key)
		} else {
			d.breaker.RecordFailure(key, err)
		}
	}
}
