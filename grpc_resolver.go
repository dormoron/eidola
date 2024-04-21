package eidola

import (
	"context"
	"eidola/registry"
	"google.golang.org/grpc/resolver"
	"time"
)

type GrpcResolverOptions func(r *GrpcResolverBuilder)

type GrpcResolverBuilder struct {
	registry registry.Registry
	timeout  time.Duration
}

func NewRegistryBuilder(registry registry.Registry, opts ...GrpcResolverOptions) (*GrpcResolverBuilder, error) {
	res := &GrpcResolverBuilder{
		registry: registry,
		timeout:  time.Second * 10,
	}
	for _, opt := range opts {
		opt(res)
	}
	return res, nil
}

func RegistryWithTimeout(timeout time.Duration) GrpcResolverOptions {
	return func(r *GrpcResolverBuilder) {
		r.timeout = timeout
	}
}

func (b *GrpcResolverBuilder) Build(target resolver.Target, clientConn resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	res := &grpcResolver{
		target:     target,
		registry:   b.registry,
		clientConn: clientConn,
		timeout:    b.timeout,
	}
	res.resolve()
	go res.watch()
	return res, nil
}

func (b *GrpcResolverBuilder) Scheme() string {
	return "registry"
}

type grpcResolver struct {
	target     resolver.Target
	registry   registry.Registry
	clientConn resolver.ClientConn
	timeout    time.Duration
	close      chan struct{}
}

func (g *grpcResolver) ResolveNow(options resolver.ResolveNowOptions) {
	g.resolve()
}

func (g *grpcResolver) watch() {
	ch, err := g.registry.Subscribe(g.target.Endpoint())
	if err != nil {
		g.clientConn.ReportError(err)
		return
	}
	for {
		select {
		case _ = <-ch:
			g.resolve()
		case <-g.close:
			return
		}
	}
}

func (g *grpcResolver) resolve() {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	instances, err := g.registry.ListService(ctx, g.target.Endpoint())
	if err != nil {
		g.clientConn.ReportError(err)
		return
	}
	address := make([]resolver.Address, 0, len(instances))
	for _, si := range instances {
		address = append(address, resolver.Address{
			Addr: si.Address,
		})
	}
	err = g.clientConn.UpdateState(resolver.State{
		Addresses: address,
	})
	if err != nil {
		g.clientConn.ReportError(err)
		return
	}
}

func (g *grpcResolver) Close() {
	close(g.close)
}
