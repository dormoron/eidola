package broadcast

import (
	"context"
	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc"
	"reflect"
	"sync"
)

// Response structure for holding either a reply or an error.
type Response struct {
	Error error
	Reply interface{}
}

// broadcastKey used as a unique type for context values.
type broadcastKey struct{}

// ClusterBuilder struct for building client interceptors with distributed service discovery.
type ClusterBuilder struct {
	registry    registry.Registry // Interface for service discovery
	service     string            // Name of the service for discovery
	dialOptions []grpc.DialOption // GRPC dial options
}

// NewClusterBuilder creates a new ClusterBuilder instance.
func NewClusterBuilder(registry registry.Registry, service string, dialOptions ...grpc.DialOption) *ClusterBuilder {
	return &ClusterBuilder{
		registry:    registry,
		service:     service,
		dialOptions: dialOptions,
	}
}

// BuildUnaryInterceptor constructs a unary client interceptor for broadcasting RPCs across instances.
func (b *ClusterBuilder) BuildUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ok, ch := isBroadcast(ctx)
		if !ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		defer close(ch)

		instances, err := b.registry.ListServices(ctx, b.service)
		if err != nil {
			return err
		}

		var wg sync.WaitGroup
		replyType := reflect.TypeOf(reply).Elem()
		wg.Add(len(instances))

		for _, instance := range instances {
			instance := instance // Create new instance of instance for the goroutine to capture.
			go func() {
				defer wg.Done()
				instanceCC, err := grpc.Dial(instance.Address, b.dialOptions...)
				if err != nil {
					ch <- Response{Error: err}
					return
				}
				defer instanceCC.Close()

				newReply := reflect.New(replyType).Interface()
				err = invoker(ctx, method, req, newReply, instanceCC, opts...)
				select {
				case <-ctx.Done():
					err = errs.ErrClusterNoResponse(ctx.Err())
				case ch <- Response{Error: err, Reply: newReply}:
				}
			}()
		}
		wg.Wait()
		return nil
	}
}

// UseBroadcast activates broadcasting for an RPC call, returning a context and a channel to receive the responses.
func UseBroadcast(ctx context.Context) (context.Context, <-chan Response) {
	ch := make(chan Response)
	return context.WithValue(ctx, broadcastKey{}, ch), ch
}

// isBroadcast checks if the context has broadcasting enabled.
func isBroadcast(ctx context.Context) (bool, chan Response) {
	val, ok := ctx.Value(broadcastKey{}).(chan Response)
	return ok, val
}
