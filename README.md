# Eidola Framework

> 一个高性能、生产级别的Go语言微服务框架，专为系统弹性、可观测性和卓越性能而设计

## 🚀 项目概述

Eidola是一个面向生产环境的Go微服务框架，它整合了现代云原生应用所需的各种核心功能，包括服务注册发现、负载均衡、限流熔断、可观测性和安全认证等。框架名称"Eidola"源自希腊语，意为"幻影"，象征着轻量、高效且可靠的服务架构。

### 设计理念

- **高性能**：从底层设计优化，减少GC压力，提高吞吐量
- **高可靠**：内置多种弹性设计模式，确保系统在高负载和故障情况下的稳定性
- **可扩展**：模块化架构和插件系统，方便定制和扩展
- **开发友好**：简洁API和完善文档，降低使用门槛

## ✨ 核心特性

Eidola框架提供了丰富的功能，帮助您构建可靠、高性能的微服务：

### 服务治理

- **服务注册与发现**：支持etcd作为注册中心，自动服务健康检查
- **负载均衡**：内置多种负载均衡算法（轮询、随机、最少活跃连接、一致性哈希等）
- **服务路由**：支持基于标签、权重的动态路由策略

### 弹性设计

- **自适应限流**：根据系统负载动态调整限流阈值，智能保护系统资源
- **自适应熔断**：自动分析错误模式并调整熔断参数，提高系统弹性
- **智能重试**：基于退避算法的重试机制，避免雪崩效应
- **连接池管理**：优化的连接池实现，减少连接建立开销

### 性能优化

- **批处理请求合并**：智能批处理机制，减少网络往返，优化系统吞吐量
- **内存对象池**：类型安全的对象池实现，减少GC压力，提升性能
- **数据压缩**：多种压缩算法支持，优化网络传输效率

### 可观测性

- **分布式追踪**：基于OpenTelemetry的全链路追踪实现
- **指标监控**：与Prometheus集成的度量指标收集
- **结构化日志**：统一的日志格式和级别控制

### 安全特性

- **多层认证授权**：支持JWT、TLS和基于角色的访问控制(RBAC)
- **敏感字段加密**：字段级别的加密功能，保护敏感数据安全
- **增强错误处理**：结构化错误信息，支持错误分类、严重程度和恢复策略

## 🔨 快速开始

### 安装

```bash
go get github.com/dormoron/eidola
```

### 创建服务端

```go
package main

import (
    "log"
    
    "github.com/dormoron/eidola"
    "github.com/dormoron/eidola/registry/etcd"
)

func main() {
    // 创建etcd注册中心
    registry, err := etcd.NewRegistry(etcd.Config{
        Endpoints: []string{"localhost:2379"},
    })
    if err != nil {
        log.Fatalf("创建注册中心失败: %v", err)
    }
    
    // 创建服务器
    server, err := eidola.NewServer("user-service",
        eidola.ServerWithRegistry(registry),
        eidola.ServerWithGracefulStop(true),
    )
    if err != nil {
        log.Fatalf("创建服务器失败: %v", err)
    }
    
    // 注册服务实现
    // pb.RegisterUserServiceServer(server.Server, &UserService{})
    
    // 启动服务器
    if err := server.Start(":50051"); err != nil {
        log.Fatalf("启动服务器失败: %v", err)
    }
}
```

### 创建客户端

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/dormoron/eidola"
    "github.com/dormoron/eidola/internal/circuitbreaker"
    "github.com/dormoron/eidola/registry/etcd"
)

func main() {
    // 创建etcd注册中心
    registry, err := etcd.NewRegistry(etcd.Config{
        Endpoints: []string{"localhost:2379"},
    })
    if err != nil {
        log.Fatalf("创建注册中心失败: %v", err)
    }
    
    // 创建客户端
    client, err := eidola.NewClient(
        eidola.ClientWithResolver(registry, 10*time.Second),
        eidola.ClientWithCircuitBreaker(circuitbreaker.DefaultOptions()),
        eidola.ClientInsecure(),
    )
    if err != nil {
        log.Fatalf("创建客户端失败: %v", err)
    }
    
    // 连接到服务
    conn, err := client.Dial(context.Background(), "user-service")
    if err != nil {
        log.Fatalf("连接服务失败: %v", err)
    }
    defer conn.Close()
    
    // 创建服务客户端
    // userClient := pb.NewUserServiceClient(conn)
    
    // 调用服务方法
    // ...
}
```

## 📚 核心组件详解

### 自适应限流

基于系统负载和响应时间动态调整限流阈值，比传统固定阈值限流器更能适应变化的环境。

```go
// 创建自适应限流器
limiter := ratelimit.NewAdaptiveLimiter(ratelimit.AdaptiveLimiterOptions{
    InitialRate: 100,               // 初始QPS
    MinRate:     10,                // 最低QPS
    MaxRate:     1000,              // 最高QPS
    HighLoadThreshold: 0.75,        // 75%CPU使用率视为高负载
    LowLoadThreshold:  0.50,        // 50%CPU使用率视为低负载
    ResponseTimeThreshold: 100 * time.Millisecond,
})

// 限流检查
if err := limiter.AllowWithContext(ctx); err != nil {
    return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded: %v", err)
}
```

### 增强错误处理

结构化错误信息，支持错误分类、严重程度和恢复策略，便于问题排查和系统监控。

```go
// 创建增强错误
err := errs.NewEnhancedError(
    "USER_NOT_FOUND",               // 错误代码
    "用户不存在",                     // 错误消息
    errs.SeverityError,             // 错误严重程度
    errs.CategoryBusiness,          // 错误类别
)

// 添加元数据
err.WithMetadata("user_id", "12345")
   .WithRecoveryStrategy(errs.RecoveryRetry)

// 从现有错误创建增强错误
if dbErr != nil {
    enhancedErr := errs.FromError(
        dbErr, 
        "DB_ERROR", 
        errs.SeverityCritical, 
        errs.CategoryDatabase,
    )
    return nil, enhancedErr
}
```

### 批处理请求合并

智能合并多个请求为一个批处理操作，减少网络往返，优化系统吞吐量。

```go
// 创建批处理器
processor := func(ctx context.Context, batchReq interface{}) (interface{}, error) {
    requests := batchReq.([]interface{})
    // 批量处理逻辑
    return results, nil
}

batcher := batch.NewAdaptiveBatcher(processor, batch.AdaptiveBatchOptions{
    MaxBatchSize: 100,              // 最大批处理大小
    MaxLatency:   50 * time.Millisecond, // 最大等待时间
})

// 处理请求
result, err := batcher.Process(ctx, request)
```

### 字段级加密

提供字段级别的加密功能，保护敏感数据安全，支持自动加密和掩码处理。

```go
// 创建字段加密器
encryptor, _ := security.NewFieldEncryptor([]byte("encryption-key-12345678901234"))

// 注册需要加密的类型和字段
encryptor.RegisterType(&UserInfo{}, []string{
    "Password",                    // 完全加密
    "CreditCard:mask",             // 掩码处理
})

// 加密数据
user := &UserInfo{
    Name:       "张三",
    Password:   "secret123",
    CreditCard: "1234-5678-9012-3456",
}
encryptor.Encrypt(user)  // Password被加密，CreditCard被掩码处理为"****-****-****-3456"
```

## 📦 项目结构

```
eidola/
├── auth/             # 认证与授权模块
│   ├── authorization/ # 授权相关实现
│   ├── mfa/          # 多因素认证支持
│   └── tls/          # TLS安全配置
├── cluster/          # 集群管理
├── examples/         # 示例代码
├── internal/         # 内部实现
│   ├── batch/        # 请求批处理
│   ├── circuitbreaker/ # 断路器实现
│   ├── compression/  # 数据压缩
│   ├── connpool/     # 连接池
│   ├── errs/         # 增强错误处理
│   ├── mempool/      # 内存对象池
│   ├── retry/        # 重试策略
│   ├── security/     # 安全相关
│   ├── serialization/ # 序列化
│   └── stream/       # 流处理
├── loadbalance/      # 负载均衡
│   ├── fastest/      # 最快响应
│   ├── hash/         # 一致性哈希
│   ├── leastactive/  # 最少活跃连接
│   ├── random/       # 随机选择
│   └── round_robin/  # 轮询
├── observability/    # 可观测性
│   ├── logging/      # 日志
│   ├── metrics/      # 指标
│   └── opentelemetry/ # 分布式追踪
├── plugin/           # 插件系统
├── ratelimit/        # 限流实现
│   └── lua/          # Redis Lua脚本
├── registry/         # 服务注册
│   └── etcd/         # Etcd实现
├── client.go         # 客户端实现
├── resolver.go       # 服务解析器
└── server.go         # 服务端实现
```

## 🔐 安全性建议

1. **传输安全**
   - 生产环境始终启用TLS加密
   - 定期轮换证书
   - 配置适当的密码套件

2. **认证与授权**
   - 对敏感操作实施多因素认证
   - 使用细粒度的RBAC权限控制
   - 敏感数据始终使用字段级加密

3. **限流与保护**
   - 为所有公开API配置适当的限流
   - 使用自适应限流应对流量波动
   - 对关键资源实施熔断保护

## 📈 性能调优

1. **连接管理**
   - 根据服务规模调整连接池大小
   - 启用连接空闲超时，避免资源浪费
   - 使用自适应连接池应对负载变化

2. **请求处理**
   - 启用批处理合并相似请求
   - 使用内存对象池减少垃圾回收压力
   - 对大型负载启用压缩

3. **资源限制**
   - 设置适当的并发流限制
   - 配置消息大小上限防止资源耗尽
   - 实施请求超时避免长时间阻塞

## 🔍 可观测性

1. **度量指标**
   - 收集服务级别指标 (请求率、错误率、延迟)
   - 监控系统资源 (CPU、内存、网络)
   - 设置适当的告警阈值

2. **分布式追踪**
   - 启用OpenTelemetry追踪所有服务间调用
   - 对关键路径进行详细追踪
   - 分析性能瓶颈和异常路径

3. **日志管理**
   - 使用结构化日志格式
   - 集中收集和分析日志
   - 实施日志级别控制减少噪音

## 🤝 贡献指南

我们欢迎各种形式的贡献，包括但不限于：

- 提交问题和功能请求
- 改进文档
- 提交代码修复或新功能
- 添加测试用例
- 分享使用经验

请遵循以下步骤：

1. Fork项目
2. 创建您的特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交您的更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 开启一个Pull Request

## 📄 许可证

此项目基于 MIT 许可证 - 详情请查看 [LICENSE](LICENSE) 文件。

## 👥 维护者

- [Dormoron](https://github.com/dormoron)

---

*建设更可靠、高效的微服务架构*