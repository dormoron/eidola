package broadcast

import (
	"context"
	"eidola/registry"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

type ClusterBuilder struct {
	registry    registry.Registry
	service     string
	dialOptions []grpc.DialOption
}

func NewClusterBuilder(registry registry.Registry, service string, dialOptions ...grpc.DialOption) *ClusterBuilder {
	return &ClusterBuilder{
		registry:    registry,
		service:     service,
		dialOptions: dialOptions,
	}
}

func (b ClusterBuilder) BuildUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if !isBroadCast(ctx) {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		instances, err := b.registry.ListServices(ctx, b.service)
		if err != nil {
			return err
		}
		var eg errgroup.Group
		for _, ins := range instances {
			addr := ins.Address
			eg.Go(func() error {
				insCC, er := grpc.Dial(addr, b.dialOptions...)
				if er != nil {
					return er
				}
				return invoker(ctx, method, req, reply, insCC, opts...)
			})

		}
		return eg.Wait()
	}
}

func UseBroadCast(ctx context.Context) context.Context {
	return context.WithValue(ctx, broadcastKey{}, true)
}

type broadcastKey struct{}

func isBroadCast(ctx context.Context) bool {
	val, ok := ctx.Value(broadcastKey{}).(bool)
	return ok && val
}
