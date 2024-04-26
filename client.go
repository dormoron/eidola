package eidola

import (
	"context"
	"fmt"
	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/credentials/insecure"
	"time"
)

// ClientOption defines an option for configuring the Client.
type ClientOption func(c *Client)

// Client represents the configuration for a gRPC client.
type Client struct {
	insecure bool
	registry registry.Registry
	timeout  time.Duration
	balancer balancer.Builder
}

// NewClient initializes a new gRPC client with the provided options.
func NewClient(opts ...ClientOption) (*Client, error) {
	c := &Client{
		timeout: time.Second * 3, // Default timeout
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

// Dial creates a client connection to the given target.
func (c *Client) Dial(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	// Configure service resolution if a registry is provided.
	if c.registry != nil {
		registryBuilder, err := NewRegistryBuilder(c.registry, RegistryWithTimeout(c.timeout))
		if err != nil {
			return nil, errs.ErrClientCreateRegistry(err)
		}
		opts = append(opts, grpc.WithResolvers(registryBuilder))
	}

	// Use insecure credentials if configured to do so.
	if c.insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Set the load balancing strategy if specified.
	if c.balancer != nil {
		serviceConfig := fmt.Sprintf(`{"LoadBalancingPolicy": "%s"}`, c.balancer.Name())
		opts = append(opts, grpc.WithDefaultServiceConfig(serviceConfig))
	}

	// Append any additional dial options.
	opts = append(opts, dialOptions...)

	// Establish the connection.
	clientConn, err := grpc.DialContext(ctx, fmt.Sprintf("registry:///%s", target), opts...)
	if err != nil {
		return nil, errs.ErrClientDial(target, err)
	}
	return clientConn, nil
}
