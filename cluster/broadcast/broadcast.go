package broadcast

import (
	"context"
	"github.com/dormoron/eidola/registry"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// broadcastKey used as a unique type for context values.
type broadcastKey struct{}

// ClusterBuilder is a structure for building and handling
// RPC calls within a cluster of services.
type ClusterBuilder struct {
	registry    registry.Registry
	service     string
	dialOptions []grpc.DialOption
}

// NewClusterBuilder is a constructor for ClusterBuilder
func NewClusterBuilder(registry registry.Registry, service string, dialOptions ...grpc.DialOption) *ClusterBuilder {
	return &ClusterBuilder{
		registry:    registry,
		service:     service,
		dialOptions: dialOptions,
	}
}

// BuildUnaryInterceptor builds a unary interceptor for broadcast
func (b *ClusterBuilder) BuildUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if !isBroadCast(ctx) {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		instances, err := b.registry.ListServices(ctx, b.service)
		if err != nil {
			return err
		}

		errGroup, ctx := errgroup.WithContext(ctx)
		for _, ins := range instances {
			addr := ins.Address
			errGroup.Go(func() error {
				instanceCC, err := grpc.DialContext(ctx, addr, b.dialOptions...)
				if err != nil {
					return err
				}
				defer instanceCC.Close()
				return invoker(ctx, method, req, reply, instanceCC, opts...)
			})
		}

		return errGroup.Wait()
	}
}

// UseBroadCast returns a new context with broadcast enabled
func UseBroadCast(ctx context.Context) context.Context {
	return context.WithValue(ctx, broadcastKey{}, true)
}

// isBroadCast checks if the context has broadcast enabled
func isBroadCast(ctx context.Context) bool {
	val, ok := ctx.Value(broadcastKey{}).(bool)
	return ok && val
}
