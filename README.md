# Eidola

> 一个高性能、生产级别的Go语言gRPC微服务框架，专为可靠性、可观测性和可扩展性而设计。

## 优化小结

我们对Eidola框架进行了全面优化，使其更适合生产环境：

- **服务端优化**：添加了优雅关闭、健康检查、连接管理和并发控制
- **客户端优化**：增强了重试机制、负载均衡、连接池和错误处理
- **服务发现优化**：改进了服务解析器，提高了稳定性和可靠性
- **可观测性加强**：集成了OpenTelemetry和Prometheus，支持全链路追踪和指标监控
- **错误处理增强**：完善了错误处理机制，提供了更详细的错误信息
- **日志系统集成**：添加了结构化日志支持，便于问题排查
- **性能调优**：优化了连接管理、资源控制和消息处理
- **认证授权增强**：添加了JWT高级功能，包括自动令牌刷新、黑名单机制、令牌轮换和增强验证

这些优化使Eidola框架成为构建可靠、高性能微服务的理想选择。

## 介绍

Eidola 是一个用Go语言编写的高性能微服务框架，专用于解决在微服务体系结构中常见的一些问题，包括服务注册、服务发现，以及简化微服务之间的通信。框架经过生产环境优化，提供了丰富的功能和可靠的性能保障。

## 特色
- **服务注册与发现**：支持自动注册服务实例到注册中心，并提供高效的服务发现机制
- **负载均衡**：内置多种负载均衡策略（轮询、随机、一致性哈希、最少活跃连接等）
- **服务限流**：支持多种限流算法（令牌桶、漏桶、滑动窗口等），防止过载
- **服务路由**：根据客户端请求特性智能路由到合适的服务实例
- **分布式跟踪**：集成OpenTelemetry，提供全链路追踪能力
- **指标监控**：集成Prometheus，提供丰富的性能指标监控
- **连接管理**：支持连接池、超时控制、重试机制和优雅关闭
- **安全通信**：支持TLS加密和身份认证
- **健康检查**：内置健康检查服务，支持服务状态监控
- **高级认证授权**：提供JWT高级功能，增强系统安全性和用户体验

## 生产环境优化
- **高可用性**：自动重试、故障转移和服务降级
- **高性能**：连接池管理、流控制和资源限制
- **可观测性**：全链路追踪、指标监控和日志收集
- **安全性**：TLS加密、认证授权和数据保护
- **可靠性**：优雅启动和关闭、错误处理和恢复机制

## 生产环境中的最佳实践

使用Eidola框架在生产环境中构建微服务时，建议遵循以下最佳实践：

### 服务配置

1. **超时控制**：为每个服务设置合适的超时参数，避免无限等待。
   ```go
   server := eidola.NewServer(
       "user-service",
       eidola.ServerWithShutdownTimeout(time.Second*20), // 关闭超时
   )
   
   client := eidola.NewClient(
       eidola.ClientWithResolver(registry, time.Second*3), // 解析超时
   )
   ```

2. **重试策略**：根据服务特性配置重试策略，避免过多重试导致雪崩。
   ```go
   client := eidola.NewClient(
       eidola.ClientWithRetry(eidola.RetryConfig{
           Enabled:     true,
           MaxAttempts: 3,             // 最多尝试3次
           MaxBackoff:  time.Second*2, // 最长退避2秒
           BaseBackoff: time.Millisecond*100, // 初始退避100毫秒
       }),
   )
   ```

3. **资源限制**：设置合理的资源限制，防止资源耗尽。
   ```go
   server := eidola.NewServer(
       "user-service",
       eidola.ServerWithStreamConfig(
           50,             // 每个连接最多50个并发流
           512*1024,       // 流初始窗口大小512KB
           1024*1024*2,    // 连接初始窗口大小2MB
       ),
   )
   ```

### 高可用性

1. **服务注册与发现**：使用可靠的注册中心，如etcd或Consul。
   ```go
   etcdClient, _ := clientv3.New(clientv3.Config{
       Endpoints: []string{"etcd-1:2379", "etcd-2:2379", "etcd-3:2379"}, // 多节点冗余
       DialTimeout: time.Second * 5,
   })
   registry, _ := etcd.NewRegistry(etcdClient)
   ```

2. **负载均衡**：根据服务特性选择合适的负载均衡策略。
   ```go
   // 对于无状态服务，可以使用轮询或随机
   client.ClientWithPickerBuilder("round_robin", round_robin.NewBuilder())
   
   // 对于有状态服务，可以使用一致性哈希
   client.ClientWithPickerBuilder("consistent_hash", hash.NewBuilder())
   ```

3. **容错设计**：使用熔断和降级机制处理异常情况。
   ```go
   // 实现熔断拦截器
   breaker := circuitbreaker.NewBreaker()
   interceptor := func(ctx context.Context, method string, req, reply interface{}, 
                      cc *grpc.ClientConn, invoker grpc.UnaryInvoker, 
                      opts ...grpc.CallOption) error {
       if !breaker.Allow() {
           return status.Error(codes.Unavailable, "service is unavailable")
       }
       err := invoker(ctx, method, req, reply, cc, opts...)
       breaker.Record(err == nil)
       return err
   }
   ```

### 可观测性

1. **分布式追踪**：集成OpenTelemetry进行全链路追踪。
   ```go
   // 创建OpenTelemetry拦截器
   otelInterceptor := opentelemetry.NewServerInterceptorBuilder(
       "myapp", "grpc", "server", "GRPC server tracing",
   ).WithTimeout(time.Second * 30).BuildUnary()
   
   // 添加到服务器
   grpc.UnaryInterceptor(otelInterceptor)
   ```

2. **指标监控**：使用Prometheus收集关键指标。
   ```go
   // 创建Prometheus指标拦截器
   metricsInterceptor := metrics.ServerMetricsBuilder{
       Namespace: "myapp",
       Subsystem: "grpc",
       Name:      "server",
       Help:      "GRPC server metrics",
   }.Build()
   
   // 添加到服务器
   grpc.UnaryInterceptor(metricsInterceptor)
   ```

3. **日志收集**：实现结构化日志，便于聚合和分析。
   ```go
   // 配置结构化日志并集成到服务中
   logger := log.NewJSONLogger(log.NewSyncWriter(os.Stdout))
   logger = log.With(logger, "service", "user-service", "timestamp", log.DefaultTimestampUTC)
   
   // 创建日志拦截器
   logInterceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
       start := time.Now()
       resp, err := handler(ctx, req)
       logger.Log("method", info.FullMethod, "duration", time.Since(start), "error", err)
       return resp, err
   }
   ```

### 安全性

1. **TLS加密**：在生产环境中始终启用TLS。
   ```go
   // 服务端TLS
   creds, _ := credentials.NewServerTLSFromFile("server.crt", "server.key")
   server := eidola.NewServer("secure-service", eidola.ServerWithTLS(creds))
   
   // 客户端TLS
   creds, _ := credentials.NewClientTLSFromFile("ca.crt", "")
   client := eidola.NewClient(eidola.ClientWithTLS(creds))
   ```

2. **认证授权**：实现身份验证和访问控制。
   ```go
   // 使用Casbin进行权限控制
   enforcer, _ := casbin.NewEnforcer("rbac_model.conf", "rbac_policy.csv")
   authInterceptor := auth.NewAuthInterceptor(enforcer)
   
   // 添加JWT验证
   jwtManager := auth.NewJWTManager("secret-key", time.Hour)
   jwtInterceptor := auth.NewJWTInterceptor(jwtManager)
   ```

3. **敏感数据保护**：加密敏感信息，控制访问范围。
   ```go
   // 实现数据加密拦截器
   encryptInterceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
       // 解密请求中的敏感字段
       decryptSensitiveFields(req)
       resp, err := handler(ctx, req)
       // 加密响应中的敏感字段
       encryptSensitiveFields(resp)
       return resp, err
   }
   ```

### 性能优化

1. **连接池管理**：控制连接数量，避免资源浪费。
   ```go
   client := grpc.Dial(
       target,
       grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4*1024*1024)), // 4MB最大消息大小
       grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(4*1024*1024)), // 4MB最大消息大小
   )
   ```

2. **消息压缩**：对大消息启用压缩，减少网络开销。
   ```go
   client := grpc.Dial(
       target,
       grpc.WithDefaultCallOptions(grpc.UseCompressor("gzip")),
   )
   ```

3. **批处理和异步处理**：对于IO密集型操作，使用批处理减少网络往返。
   ```go
   // 批量处理请求
   results, err := client.BatchProcess(ctx, &BatchRequest{
       Items: []*Item{item1, item2, item3},
   })
   ```

通过遵循这些最佳实践，您可以充分利用Eidola框架的特性，构建稳定、可靠、高性能的微服务系统。

## 认证授权

Eidola 框架提供强大而灵活的认证授权系统，支持多种认证方式和授权策略。

### 认证方式

1. **JWT 认证**：基于 JSON Web Token 的认证机制，适合分布式系统。

```go
// 创建 JWT 令牌管理器
tokenManager := jwt.NewJWTManager([]byte("your-secret-key"), jwt.JWTConfig{
    Expiry:   time.Hour,
    Issuer:   "your-app",
    Audience: "your-service",
})

// 创建 JWT 认证器
authenticator := jwt.NewJWTAuthenticator(tokenManager)

// 创建令牌提取器（从 HTTP 头中提取）
extractor := jwt.NewHeaderTokenExtractor("authorization")
```

2. **基本认证**：使用用户名密码的基本认证方式。

### JWT 高级功能

Eidola 框架提供了四个强大的 JWT 高级功能，为生产环境中的认证授权系统提供更高级的安全保障和用户体验。

#### 1. 自动令牌刷新

自动令牌刷新功能能够在令牌即将过期前自动为用户更新令牌，避免用户会话中断，提升用户体验。

特性：
- 可配置刷新阈值和检查间隔
- 支持回调通知令牌变更
- 线程安全设计，适合并发环境
- 支持自定义刷新策略

```go
// 创建令牌刷新器
refresher := jwt.NewTokenRefresher(
    tokenManager,
    jwt.WithRefreshThreshold(time.Minute*5), // 在过期前5分钟刷新
    jwt.WithCheckInterval(time.Minute),      // 每分钟检查一次
)

// 启动令牌刷新器
err := refresher.Start(context.Background())
if err != nil {
    log.Fatalf("无法启动令牌刷新器: %v", err)
}
defer refresher.Stop()

// 创建客户端
client := jwt.NewAutoRefreshClient(initialToken, refresher)

// 注册令牌刷新回调
refreshCh, err := client.RegisterToken(func(oldToken, newToken string) {
    fmt.Printf("令牌已刷新: %s -> %s\n", oldToken, newToken)
    // 在这里更新客户端状态或通知用户
})
if err != nil {
    log.Fatalf("无法注册令牌: %v", err)
}
defer client.UnregisterToken(refreshCh)

// 获取当前令牌
currentToken := client.GetCurrentToken()
```

#### 2. 令牌黑名单

令牌黑名单功能允许即时撤销已颁发的令牌，防止令牌被滥用，增强系统安全性。

特性：
- 支持内存和分布式存储（如Redis）
- 自动清理过期令牌
- 高性能设计，支持高并发
- 可与验证流程无缝集成

```go
// 创建黑名单存储
blacklistStorage := jwt.NewMemoryBlacklistStorage(time.Minute * 5) // 清理间隔

// 创建令牌黑名单
blacklist := jwt.NewTokenBlacklist(tokenManager, blacklistStorage)

// 创建带黑名单的认证器
blacklistAuthenticator := jwt.NewBlacklistedJWTAuthenticator(tokenManager, blacklist)

// 将令牌加入黑名单
err := blacklist.Add(context.Background(), token)
if err != nil {
    log.Fatalf("无法将令牌加入黑名单: %v", err)
}

// 验证令牌时会自动检查黑名单
user, err := blacklistAuthenticator.Authenticate(context.Background(), creds)
if err != nil {
    if errors.Is(err, jwt.ErrTokenBlacklisted) {
        fmt.Println("令牌已被撤销")
    } else {
        fmt.Printf("认证失败: %v\n", err)
    }
}
```

#### 3. 密钥轮换

密钥轮换功能支持定期更换签名密钥，提高系统安全性，遵循安全最佳实践。

特性：
- 支持多种轮换策略（定时、按需、混合）
- 密钥重叠期，确保平滑过渡
- 支持密钥ID跟踪和历史密钥验证
- 可集成外部密钥管理系统

```go
// 创建密钥存储
keyStorage := jwt.NewMemoryKeyStorage()

// 创建密钥轮换器
rotator := jwt.NewTokenRotator(
    keyStorage,           // 密钥存储
    32,                   // 密钥长度（字节）
    jwt.RotateHybrid,     // 轮换策略
    time.Hour*24,         // 轮换间隔（每24小时）
    time.Hour,            // 密钥重叠期（1小时）
)

// 启动轮换器
err := rotator.Start(context.Background())
if err != nil {
    log.Fatalf("无法启动密钥轮换器: %v", err)
}
defer rotator.Stop()

// 创建带轮换的JWT管理器
rotatingManager := jwt.NewRotatingJWTManager(rotator, jwtConfig)
err = rotatingManager.Init(context.Background())
if err != nil {
    log.Fatalf("无法初始化轮换JWT管理器: %v", err)
}

// 生成令牌
token, err := rotatingManager.GenerateToken(user)
if err != nil {
    log.Fatalf("无法生成令牌: %v", err)
}

// 验证令牌
claims, err := rotatingManager.ValidateToken(token)
if err != nil {
    log.Fatalf("无法验证令牌: %v", err)
}

// 手动触发轮换
err = rotator.RotateNow(context.Background())
if err != nil {
    log.Fatalf("密钥轮换失败: %v", err)
}
```

#### 4. 增强令牌验证

增强令牌验证支持更严格的令牌验证规则，包括IP地址验证、客户端指纹验证、自定义声明验证等。

特性：
- 模块化验证规则，便于组合和扩展
- 支持标准JWT字段验证（nbf、exp、iss、aud等）
- 支持高级验证（IP地址、设备指纹、地理位置等）
- 可自定义验证逻辑

```go
// 创建增强验证器
enhancedValidator := jwt.NewEnhancedValidator(tokenManager, blacklist)

// 添加验证规则
enhancedValidator.AddRule(jwt.NotBeforeValidator())                    // 验证nbf字段
enhancedValidator.AddRule(jwt.ExpiryValidator())                       // 验证exp字段
enhancedValidator.AddRule(jwt.IssuerValidator("your-app"))             // 验证颁发者
enhancedValidator.AddRule(jwt.AudienceValidator("your-service"))       // 验证受众
enhancedValidator.AddRule(jwt.IssuedAtValidator(time.Hour*24*7))       // 令牌最长有效期7天
enhancedValidator.AddRule(jwt.IPAddressValidator("192.168.1.0/24"))    // 限制IP地址
enhancedValidator.AddRule(jwt.CustomClaimValidator("role", "admin", false)) // 自定义声明验证

// 创建带增强验证的认证器
enhancedAuthenticator := jwt.NewEnhancedJWTAuthenticator(enhancedValidator)

// 验证令牌
user, err := enhancedAuthenticator.Authenticate(context.Background(), creds)
if err != nil {
    fmt.Printf("增强验证失败: %v\n", err)
} else {
    fmt.Printf("验证成功: %s\n", user.Name)
}
```

### 综合使用示例

下面是一个结合多种高级功能的完整示例：

```go
// 创建基础配置
jwtConfig := jwt.JWTConfig{
    SigningMethod: jwt.DefaultJWTConfig.SigningMethod,
    Expiry:        time.Minute * 15, // 短期令牌
    RefreshExpiry: time.Hour * 24,    // 刷新令牌24小时
    Issuer:        "eidola-example",
    Audience:      "example-api",
}

// 1. 创建密钥轮换系统
keyStorage := jwt.NewMemoryKeyStorage()
rotator := jwt.NewTokenRotator(keyStorage, 32, jwt.RotateHybrid, time.Hour*24, time.Hour)
rotator.Start(context.Background())
defer rotator.Stop()

// 初始化轮换JWT管理器
tokenManager := jwt.NewRotatingJWTManager(rotator, jwtConfig)
tokenManager.Init(context.Background())

// 2. 创建黑名单系统
blacklistStorage := jwt.NewMemoryBlacklistStorage(time.Minute * 30)
blacklist := jwt.NewTokenBlacklist(tokenManager, blacklistStorage)

// 3. 创建增强验证器
validator := jwt.NewEnhancedValidator(tokenManager, blacklist)
validator.AddRule(jwt.NotBeforeValidator())
validator.AddRule(jwt.ExpiryValidator())
validator.AddRule(jwt.IssuerValidator(jwtConfig.Issuer))
validator.AddRule(jwt.AudienceValidator(jwtConfig.Audience))
validator.AddRule(jwt.IssuedAtValidator(time.Hour * 24 * 30)) // 最长30天
validator.AddRule(jwt.IPAddressValidator("10.0.0.0/8", "192.168.0.0/16")) 

// 4. 创建自动刷新系统
refresher := jwt.NewTokenRefresher(tokenManager, jwt.WithRefreshThreshold(time.Minute*5))
refresher.Start(context.Background())
defer refresher.Stop()

// 生成令牌
token, err := tokenManager.GenerateToken(user)
if err != nil {
    log.Fatalf("生成令牌失败: %v", err)
}

// 创建客户端
client := jwt.NewAutoRefreshClient(token, refresher)
client.RegisterToken(func(oldToken, newToken string) {
    fmt.Println("令牌已自动刷新")
})

// 创建认证器
authenticator := jwt.NewEnhancedJWTAuthenticator(validator)

// 后续使用...
```

这些高级功能共同构成了一个强大、安全、用户友好的认证授权系统，适用于各种复杂的生产环境场景。

## 开始使用

### 安装

```bash
go get github.com/dormoron/eidola
```

### 简单示例

下面是使用JWT认证的基本示例：

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/dormoron/eidola/auth"
    "github.com/dormoron/eidola/auth/jwt"
)

func main() {
    // 创建JWT管理器
    jwtManager := jwt.NewJWTManager([]byte("example-signing-key"))

    // 创建用户
    user := &auth.User{
        ID:   "user123",
        Name: "测试用户",
        Roles: []string{
            "admin",
            "user",
        },
        Permissions: []string{
            "read",
            "write",
            "delete",
        },
        Metadata: map[string]interface{}{
            "email": "test@example.com",
        },
    }

    // 生成令牌
    token, err := jwtManager.CreateToken(user, time.Hour)
    if err != nil {
        log.Fatalf("生成令牌失败: %v", err)
    }
    fmt.Printf("生成的令牌: %s\n", token)

    // 验证令牌
    claims, err := jwtManager.ValidateToken(token)
    if err != nil {
        log.Fatalf("验证令牌失败: %v", err)
    }

    fmt.Println("验证成功！令牌中的信息:")
    fmt.Printf("用户ID: %s\n", claims["uid"].(string))
    fmt.Printf("用户名: %s\n", claims["username"].(string))
    fmt.Printf("角色: %v\n", claims["roles"].([]string))
    fmt.Printf("权限: %v\n", claims["permissions"].([]string))
}
```

## 许可证

Eidola 框架遵循 [MIT 许可证](LICENSE)。

## 贡献

欢迎对 Eidola 框架进行贡献！请阅读[贡献指南](CONTRIBUTING.md)了解详情。

## 联系我们

如有问题，请通过 [GitHub Issues](https://github.com/dormoron/eidola/issues) 联系我们。