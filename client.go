package eidola

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dormoron/eidola/internal/circuitbreaker"
	"github.com/dormoron/eidola/internal/connpool"
	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// ClientOption defines an option for configuring the Client.
type ClientOption func(c *Client)

// Client represents the configuration for a gRPC client.
type Client struct {
	insecure              bool
	registry              registry.Registry
	timeout               time.Duration
	balancer              balancer.Builder
	tls                   credentials.TransportCredentials
	retryConfig           RetryConfig
	keepaliveParams       keepalive.ClientParameters
	maxConcurrentStreams  uint32
	initialWindowSize     int32
	initialConnWindowSize int32

	// 连接池配置
	poolConfig connpool.Options
	// 是否启用连接池
	enablePool bool
	// 连接池映射表，按服务名管理连接池
	pools map[string]connpool.Pool
	// 连接池互斥锁
	poolsMu sync.RWMutex

	// 自适应连接池配置
	adaptivePoolConfig connpool.AdaptiveOptions
	// 是否启用自适应连接池
	enableAdaptivePool bool
	// 是否预热连接池
	preconnectCount int

	// 断路器配置
	circuitBreakerConfig circuitbreaker.Options
	// 是否启用断路器
	enableCircuitBreaker bool
	// 断路器映射表，按服务名管理断路器
	circuitBreakers map[string]*circuitbreaker.CircuitBreaker
	// 断路器互斥锁
	circuitBreakersMu sync.RWMutex

	// 压缩选项
	enableCompression bool
	// 压缩算法
	compressionAlgorithm string
}

// RetryConfig defines the configuration for client-side retry.
type RetryConfig struct {
	Enabled     bool
	MaxAttempts uint
	MaxBackoff  time.Duration
	BaseBackoff time.Duration
}

// DefaultRetryConfig provides sensible default values for RetryConfig.
var DefaultRetryConfig = RetryConfig{
	Enabled:     true,
	MaxAttempts: 3,
	MaxBackoff:  time.Second * 5,
	BaseBackoff: time.Millisecond * 100,
}

// NewClient initializes a new gRPC client with the provided options.
func NewClient(opts ...ClientOption) (*Client, error) {
	c := &Client{
		timeout:     time.Second * 10, // Default timeout increased
		retryConfig: DefaultRetryConfig,
		keepaliveParams: keepalive.ClientParameters{
			Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
			Timeout:             3 * time.Second,  // wait 3 seconds for ping ack before considering the connection dead
			PermitWithoutStream: true,             // send pings even without active streams
		},
		maxConcurrentStreams:  100,
		initialWindowSize:     1024 * 1024,      // 1MB
		initialConnWindowSize: 1024 * 1024 * 10, // 10MB

		// 连接池默认配置
		poolConfig: connpool.Options{
			MaxIdle:     5,
			MaxActive:   20,
			IdleTimeout: 60 * time.Second,
			WaitTimeout: 3 * time.Second,
			Wait:        true,
		},
		pools: make(map[string]connpool.Pool),

		// 断路器默认配置
		circuitBreakerConfig: circuitbreaker.DefaultOptions(),
		circuitBreakers:      make(map[string]*circuitbreaker.CircuitBreaker),
	}
	for _, opt := range opts {
		opt(c) // Apply each ClientOption to the client.
	}
	return c, nil
}

// ClientInsecure returns a ClientOption that configures the client to use insecure connections.
func ClientInsecure() ClientOption {
	return func(c *Client) {
		c.insecure = true
	}
}

// ClientWithTLS returns a ClientOption that configures the client to use TLS credentials.
func ClientWithTLS(creds credentials.TransportCredentials) ClientOption {
	return func(c *Client) {
		c.tls = creds
	}
}

// ClientWithPickerBuilder returns a ClientOption that sets a custom picker builder for load balancing.
func ClientWithPickerBuilder(name string, b base.PickerBuilder) ClientOption {
	return func(c *Client) {
		builder := base.NewBalancerBuilder(name, b, base.Config{HealthCheck: true})
		balancer.Register(builder) // Register the balancer builder.
		c.balancer = builder
	}
}

// ClientWithResolver returns a ClientOption that configures the client to use a specific registry for service resolution.
func ClientWithResolver(registry registry.Registry, timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.registry = registry
		c.timeout = timeout
	}
}

// ClientWithRetry returns a ClientOption that configures retry policy.
func ClientWithRetry(retryConfig RetryConfig) ClientOption {
	return func(c *Client) {
		c.retryConfig = retryConfig
	}
}

// ClientWithKeepalive returns a ClientOption that configures keepalive parameters.
func ClientWithKeepalive(params keepalive.ClientParameters) ClientOption {
	return func(c *Client) {
		c.keepaliveParams = params
	}
}

// ClientWithStreamConfig returns a ClientOption that configures stream related parameters.
func ClientWithStreamConfig(maxStreams uint32, windowSize, connWindowSize int32) ClientOption {
	return func(c *Client) {
		c.maxConcurrentStreams = maxStreams
		c.initialWindowSize = windowSize
		c.initialConnWindowSize = connWindowSize
	}
}

// ClientWithConnectionPool 返回一个开启连接池的ClientOption
func ClientWithConnectionPool(poolConfig connpool.Options) ClientOption {
	return func(c *Client) {
		c.enablePool = true
		c.enableAdaptivePool = false
		c.poolConfig = poolConfig
	}
}

// ClientWithAdaptiveConnectionPool 返回一个开启自适应连接池的ClientOption
func ClientWithAdaptiveConnectionPool(adaptiveConfig connpool.AdaptiveOptions) ClientOption {
	return func(c *Client) {
		c.enablePool = true
		c.enableAdaptivePool = true
		c.adaptivePoolConfig = adaptiveConfig
	}
}

// ClientWithPreconnect 设置连接池预热连接数
func ClientWithPreconnect(count int) ClientOption {
	return func(c *Client) {
		c.preconnectCount = count
	}
}

// ClientWithCircuitBreaker 返回一个开启断路器的ClientOption
func ClientWithCircuitBreaker(cbConfig circuitbreaker.Options) ClientOption {
	return func(c *Client) {
		c.enableCircuitBreaker = true
		c.circuitBreakerConfig = cbConfig
	}
}

// ClientWithCompression 返回一个开启压缩的ClientOption
func ClientWithCompression(algorithm string) ClientOption {
	return func(c *Client) {
		c.enableCompression = true
		c.compressionAlgorithm = algorithm
	}
}

// createConnection 创建一个新的gRPC连接
func (c *Client) createConnection(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
	// 检查连接池中是否有可用连接
	if c.enablePool {
		c.poolsMu.RLock()
		pool, ok := c.pools[target]
		c.poolsMu.RUnlock()

		if ok {
			poolConn, err := pool.Get(ctx)
			if err == nil {
				return poolConn.ClientConn, nil
			}
			// 获取连接失败，尝试创建新连接
		} else {
			// 没有该目标的连接池，创建一个
			c.poolsMu.Lock()
			defer c.poolsMu.Unlock()

			// 二次检查，防止在获取锁的过程中其他goroutine已创建
			if pool, ok = c.pools[target]; ok {
				poolConn, err := pool.Get(ctx)
				if err == nil {
					return poolConn.ClientConn, nil
				}
			} else {
				// 连接工厂函数
				factory := func(ctx context.Context, target string) (*grpc.ClientConn, error) {
					return c.createRealConnection(ctx, target, dialOptions...)
				}

				// 创建连接池
				var newPool connpool.Pool
				if c.enableAdaptivePool {
					newPool = connpool.NewAdaptivePool(target, factory, c.adaptivePoolConfig)

					// 预热连接池
					if c.preconnectCount > 0 {
						if ap, ok := newPool.(*connpool.AdaptivePool); ok {
							ap.Preconnect(ctx, c.preconnectCount)
						}
					}
				} else {
					newPool = connpool.NewPool(target, factory, c.poolConfig)
				}

				c.pools[target] = newPool

				// 尝试获取连接
				poolConn, err := newPool.Get(ctx)
				if err == nil {
					return poolConn.ClientConn, nil
				}
			}
		}
	}

	// 连接池未启用或获取连接失败，直接创建新连接
	return c.createRealConnection(ctx, target, dialOptions...)
}

// createRealConnection 创建实际的gRPC连接（没有连接池时使用）
func (c *Client) createRealConnection(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	// Configure service resolution if a registry is provided.
	if c.registry != nil {
		registryBuilder, err := NewResolverBuilder(c.registry, ResolverWithTimeout(c.timeout))
		if err != nil {
			return nil, errs.ErrClientCreateRegistry(err)
		}
		opts = append(opts, grpc.WithResolvers(registryBuilder))
	}

	// Configure security credentials
	if c.tls != nil {
		opts = append(opts, grpc.WithTransportCredentials(c.tls))
	} else if c.insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		// Default to insecure for compatibility with existing users
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// 设置服务配置
	var serviceConfig map[string]interface{}

	// 设置负载均衡策略
	if c.balancer != nil {
		serviceConfig = map[string]interface{}{
			"loadBalancingPolicy": c.balancer.Name(),
		}
	} else {
		serviceConfig = make(map[string]interface{})
	}

	// 设置重试策略
	if c.retryConfig.Enabled {
		methodConfig := []map[string]interface{}{
			{
				"name": []map[string]interface{}{
					{
						"service": target,
					},
				},
				"retryPolicy": map[string]interface{}{
					"maxAttempts":       c.retryConfig.MaxAttempts,
					"initialBackoff":    c.retryConfig.BaseBackoff.String(),
					"maxBackoff":        c.retryConfig.MaxBackoff.String(),
					"backoffMultiplier": 2.0,
					"retryableStatusCodes": []string{
						"UNAVAILABLE",
						"RESOURCE_EXHAUSTED",
					},
				},
			},
		}

		serviceConfig["methodConfig"] = methodConfig
	}

	// 只有在配置了负载均衡或重试策略时才设置服务配置
	if c.balancer != nil || c.retryConfig.Enabled {
		serviceConfigJSON, err := json.Marshal(serviceConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal service config: %w", err)
		}
		opts = append(opts, grpc.WithDefaultServiceConfig(string(serviceConfigJSON)))
	}

	// Configure keepalive
	opts = append(opts, grpc.WithKeepaliveParams(c.keepaliveParams))

	// Configure stream parameters
	opts = append(opts,
		grpc.WithInitialWindowSize(c.initialWindowSize),
		grpc.WithInitialConnWindowSize(c.initialConnWindowSize),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*4)), // 4MB max message size
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(1024*1024*4)), // 4MB max message size
	)

	// 设置压缩算法
	if c.enableCompression {
		if c.compressionAlgorithm != "" {
			opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor(c.compressionAlgorithm)))
		} else {
			// 默认使用gzip
			opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor("gzip")))
		}
	}

	// Add context timeout
	opts = append(opts, grpc.WithBlock())

	// Append any additional dial options.
	opts = append(opts, dialOptions...)

	// Establish the connection.
	dialCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		// If no deadline is set in the context, add a default one
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	clientConn, err := grpc.DialContext(dialCtx, fmt.Sprintf("registry:///%s", target), opts...)
	if err != nil {
		return nil, errs.ErrClientDial(target, err)
	}
	return clientConn, nil
}

// Dial creates a client connection to the given target.
func (c *Client) Dial(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
	// 如果启用了断路器，先检查断路器状态
	if c.enableCircuitBreaker {
		if err := c.getCircuitBreaker(target).Allow(); err != nil {
			return nil, fmt.Errorf("circuit breaker rejected request: %w", err)
		}

		// 设置请求完成后更新断路器状态
		defer func() {
			// 这里不能处理函数返回的错误，因为在defer中无法获取返回值
			// 实际使用时可以通过装饰器或拦截器模式来优雅处理
		}()
	}

	// 如果启用了连接池，从连接池获取连接
	conn, err := c.createConnection(ctx, target, dialOptions...)
	if err != nil {
		// 如果是连接错误，标记断路器失败
		if c.enableCircuitBreaker {
			c.getCircuitBreaker(target).Failure()
		}
		return nil, err
	}

	// 标记断路器成功
	if c.enableCircuitBreaker {
		c.getCircuitBreaker(target).Success()
	}

	return conn, nil
}

// getPool 获取指定服务的连接池，如果不存在则创建
func (c *Client) getPool(target string, dialOptions ...grpc.DialOption) connpool.Pool {
	c.poolsMu.RLock()
	pool, exists := c.pools[target]
	c.poolsMu.RUnlock()

	if exists {
		return pool
	}

	// 如果连接池不存在，创建新的连接池
	c.poolsMu.Lock()
	defer c.poolsMu.Unlock()

	// 双重检查，防止竞争条件
	if pool, exists = c.pools[target]; exists {
		return pool
	}

	// 创建一个用于连接池的工厂函数
	factory := func(ctx context.Context, target string) (*grpc.ClientConn, error) {
		return c.createConnection(ctx, target, dialOptions...)
	}

	pool = connpool.NewPool(target, factory, c.poolConfig)
	c.pools[target] = pool

	return pool
}

// getCircuitBreaker 获取指定服务的断路器，如果不存在则创建
func (c *Client) getCircuitBreaker(target string) *circuitbreaker.CircuitBreaker {
	c.circuitBreakersMu.RLock()
	cb, exists := c.circuitBreakers[target]
	c.circuitBreakersMu.RUnlock()

	if exists {
		return cb
	}

	// 如果断路器不存在，创建新的断路器
	c.circuitBreakersMu.Lock()
	defer c.circuitBreakersMu.Unlock()

	// 双重检查，防止竞争条件
	if cb, exists = c.circuitBreakers[target]; exists {
		return cb
	}

	cb = circuitbreaker.New(target, c.circuitBreakerConfig)
	c.circuitBreakers[target] = cb

	return cb
}

// Close 关闭客户端资源
func (c *Client) Close() error {
	var errs []error

	// 关闭所有连接池
	c.poolsMu.Lock()
	for _, pool := range c.pools {
		if err := pool.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	c.pools = make(map[string]connpool.Pool)
	c.poolsMu.Unlock()

	// 清理断路器资源
	c.circuitBreakersMu.Lock()
	c.circuitBreakers = make(map[string]*circuitbreaker.CircuitBreaker)
	c.circuitBreakersMu.Unlock()

	// 如果有错误，返回第一个错误
	if len(errs) > 0 {
		return errs[0]
	}

	return nil
}
