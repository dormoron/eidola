package eidola

import (
	"context"
	"eidola/registry"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"time"
)

type ClientOption func(c *Client)

type Client struct {
	insecure bool
	registry registry.Registry
	timeout  time.Duration
}

func NewClient(opts ...ClientOption) (*Client, error) {
	res := &Client{
		timeout: time.Second * 3,
	}
	for _, opt := range opts {
		opt(res)
	}
	return res, nil
}

func ClientInsecure() ClientOption {
	return func(c *Client) {
		c.insecure = true
	}
}

func ClientWithResolver(registry registry.Registry, timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.registry = registry
		c.timeout = timeout
	}
}

func (c *Client) Dial(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	if c.registry != nil {
		registryBuild, err := NewRegistryBuilder(c.registry, RegistryWithTimeout(c.timeout))
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithResolvers(registryBuild))
	}
	if c.insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if len(dialOptions) > 0 {
		opts = append(opts, dialOptions...)
	}
	clientConn, err := grpc.DialContext(ctx, fmt.Sprintf("registry:///%s", target), opts...)
	return clientConn, err
}
