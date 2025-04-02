# Eidola 框架优化示例

本示例展示了 Eidola gRPC 微服务框架的各种优化功能的实际应用，为开发者提供了完整的参考实现。

## 功能概述

示例代码集成了以下优化功能：

1. **自适应限流(Adaptive Rate Limiting)**
   - 根据系统负载动态调整限流阈值
   - 支持按照服务、方法、客户端等维度进行限流
   - 实现了令牌桶算法，具有突发流量处理能力

2. **增强错误处理(Enhanced Error Handling)**
   - 支持错误分类、严重级别和错误上下文
   - 提供错误聚合和分析功能
   - 增强的错误恢复策略

3. **自适应断路器(Adaptive Circuit Breaker)**
   - 根据错误模式自动调整断路参数
   - 支持半开状态和渐进式恢复
   - 错误模式分析和智能阈值调整

4. **批处理请求合并(Batch Request Merging)**
   - 动态调整批处理大小和延迟
   - 智能负载感知的批处理策略
   - 支持超时控制和请求取消

5. **内存池优化(Memory Pooling)**
   - 类型安全的对象池实现
   - 减少GC压力，提高性能
   - 支持自定义对象生命周期管理

6. **敏感字段加密(Field Encryption)**
   - 支持字段级别的端到端加密
   - 提供字段掩码和传输加密
   - 基于AES-GCM的安全加密实现

7. **方法级度量指标(Method-level Metrics)**
   - 详细的请求、响应和错误指标
   - 延迟、并发和速率监控
   - 支持自定义指标收集和报告

## 运行示例

要运行此示例，请确保已安装Go 1.20或更高版本，然后执行：

```bash
# 进入示例目录
cd examples

# 运行示例
go run main.go
```

## 示例代码讲解

### 自适应限流

```go
// 创建自适应限流器
limiter := ratelimit.NewAdaptiveLimiter(ratelimit.DefaultAdaptiveLimiterOptions())

// 在请求处理中使用
if err := s.limiter.AllowWithContext(ctx); err != nil {
    return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded: %v", err)
}
```

### 增强错误处理

```go
// 创建增强错误
enhancedErr := errs.FromError(err, "PROCESS_ERROR", errs.SeverityError, errs.CategoryNetwork)

// 记录错误
errs.GetGlobalErrorAggregator().Record(enhancedErr)
```

### 自适应断路器

```go
// 创建自适应断路器
breaker := circuitbreaker.NewAdaptiveCircuitBreaker("example-service", 
    circuitbreaker.DefaultAdaptiveOptions())

// 在业务逻辑中使用
result, err := s.breaker.Execute(func() (interface{}, error) {
    // 业务逻辑
    return result, nil
})
```

### 批处理请求合并

```go
// 创建批处理器
batcher := batch.NewAdaptiveBatcher(processor, batch.DefaultAdaptiveBatchOptions())

// 使用批处理器
batchResult, batchErr := s.batcher.Process(ctx, request)
```

### 内存池优化

```go
// 创建对象池
msgPool := mempool.NewTypedMessagePool[*ExampleRequest](
    mempool.DefaultMessagePoolOptions())

// 使用对象池
pooledReq := s.msgPool.Get()
defer s.msgPool.Put(pooledReq)
```

### 敏感字段加密

```go
// 创建字段加密器
encryptor, _ := security.NewFieldEncryptor([]byte("encryption-key"))

// 注册要加密的类型和字段
encryptor.RegisterType(&ExampleRequest{}, []string{"SensitiveInfo", "UserCredential:mask"})

// 解密和加密
encryptor.Decrypt(request)
encryptor.Encrypt(response)
```

### 方法级度量指标

```go
// 创建度量收集器
metrics := metrics.NewMethodMetrics(metrics.DefaultMethodMetricsOptions())

// 记录请求和响应
ctx = metrics.RecordRequest(ctx, "/Service/Method", size, "unary")
metrics.RecordResponse(ctx, "/Service/Method", size, err, "unary")
```

## 最佳实践

- **配置调优**: 根据实际负载情况调整各个组件的参数
- **错误处理**: 使用增强错误处理提供更好的错误诊断
- **资源管理**: 合理使用内存池减少GC压力
- **安全考虑**: 对敏感数据使用字段加密
- **监控告警**: 基于方法级度量指标设置合理的监控和告警

## 下一步

- 探索更多Eidola框架的高级功能
- 查看其他示例和文档
- 在实际项目中集成这些优化功能 