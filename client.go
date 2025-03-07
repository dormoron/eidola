package eidola

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// Dial creates a client connection to the given target.
func (c *Client) Dial(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
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

	// 创建一个合并的服务配置
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
