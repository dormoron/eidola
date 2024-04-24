package broadcast

import (
	"context"
	"eidola/registry"
	"google.golang.org/grpc"
	"reflect"
	"sync"
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
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ok, ch := isBroadCast(ctx)
		if !ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		defer func() {
			close(ch)
		}()
		instances, err := b.registry.ListServices(ctx, b.service)
		if err != nil {
			return err
		}
		var wg sync.WaitGroup
		typ := reflect.TypeOf(reply).Elem()
		wg.Add(len(instances))
		for _, ins := range instances {
			addr := ins.Address
			go func() {
				insCC, er := grpc.Dial(addr, b.dialOptions...)
				if er != nil {
					ch <- Resp{Err: er}
					wg.Done()
					return
				}
				newReply := reflect.New(typ).Interface()
				err = invoker(ctx, method, req, newReply, insCC, opts...)
				// 如果没有人接收，就会堵住
				select {
				case ch <- Resp{Err: er, Reply: newReply}:
				default:

				}
				wg.Done()
			}()
		}
		wg.Wait()
		return err
	}
}

func UseBroadCast(ctx context.Context) (context.Context, <-chan Resp) {
	ch := make(chan Resp)
	return context.WithValue(ctx, broadcastKey{}, ch), ch
}

type broadcastKey struct{}

func isBroadCast(ctx context.Context) (bool, chan Resp) {
	val, ok := ctx.Value(broadcastKey{}).(chan Resp)
	return ok, val
}

type Resp struct {
	Err   error
	Reply any
}
