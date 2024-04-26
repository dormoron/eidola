package broadcast

import (
	"context"
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

// ClusterBuilder helps to communicate with a cluster of services for a given `service`.
type ClusterBuilder struct {
	registry    registry.Registry // Service registry for service discovery
	service     string            // Name of the service to communicate with
	dialOptions []grpc.DialOption // List of dial options for gRPC
}

// NewClusterBuilder creates a new instance of ClusterBuilder with the provided parameters.
func NewClusterBuilder(registry registry.Registry, service string, dialOptions ...grpc.DialOption) *ClusterBuilder {
	return &ClusterBuilder{
		registry:    registry,    // The service registry
		service:     service,     // Name of the target service
		dialOptions: dialOptions, // gRPC dial options
	}
}

// BuildUnaryInterceptor creates a grpc.UnaryClientInterceptor that supports broadcasting.
func (b *ClusterBuilder) BuildUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ok, ch := isBroadCast(ctx)
		if !ok {
			// Invoker call for non-broadcast scenarios
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		// Ensure the broadcast channel is closed when done to free resources
		defer close(ch)

		// Retrieve the instances of the service from the registry
		instances, err := b.registry.ListServices(ctx, b.service)
		if err != nil {
			return err
		}

		// Set up a wait group for concurrent goroutines
		var wg sync.WaitGroup
		replyType := reflect.TypeOf(reply).Elem()
		wg.Add(len(instances))
		for _, ins := range instances {
			instance := ins // Capture range variable
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
				// Non-blocking send to channel with 'select'
				select {
				case ch <- Response{Error: err, Reply: newReply}:
				default:
				}
			}()
		}
		// Wait for all goroutines to complete
		wg.Wait()
		return nil // We do not return 'err' as it is the last set error in a concurrent scenario
	}
}

// UseBroadCast creates a new context that indicates a broadcast should be performed.
func UseBroadCast(ctx context.Context) (context.Context, chan Response) {
	ch := make(chan Response)
	// Store the channel in context to use it for broadcasting
	return context.WithValue(ctx, broadcastKey{}, ch), ch
}

// isBroadCast checks if the context has been marked to indicate broadcasting.
func isBroadCast(ctx context.Context) (bool, chan Response) {
	// Attempt to retrieve the broadcast channel from the context
	val, ok := ctx.Value(broadcastKey{}).(chan Response)
	return ok, val
}
