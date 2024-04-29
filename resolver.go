package eidola

import (
	"context"
	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc/attributes"
	"google.golang.org/grpc/resolver"
	"time"
)

// ResolverOptions defines a functional option for configuring a ResolverBuilder.
type ResolverOptions func(r *ResolverBuilder)

// ResolverBuilder constructs a grpcResolver with registry and timeout settings.
type ResolverBuilder struct {
	registry registry.Registry
	timeout  time.Duration
}

// NewResolverBuilder creates a new ResolverBuilder and applies any additional options.
func NewResolverBuilder(registry registry.Registry, opts ...ResolverOptions) (*ResolverBuilder, error) {
	builder := &ResolverBuilder{
		registry: registry,
		timeout:  3 * time.Second, // Default timeout set to 3 seconds.
	}

	// Apply each option to the builder.
	for _, opt := range opts {
		opt(builder)
	}

	return builder, nil
}

// ResolverWithTimeout creates a ResolverOptions which sets a custom timeout for a ResolverBuilder.
func ResolverWithTimeout(timeout time.Duration) ResolverOptions {
	return func(builder *ResolverBuilder) {
		builder.timeout = timeout
	}
}

// Build constructs a grpcResolver for a given target and client connection with additional resolver build options.
func (b *ResolverBuilder) Build(target resolver.Target, clientConn resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &grpcResolver{
		target:     target,
		registry:   b.registry,
		clientConn: clientConn,
		timeout:    b.timeout,
		closeCh:    make(chan struct{}),
	}

	// Start initial resolution and the monitoring goroutine.
	r.resolve()
	go r.watch()

	return r, nil
}

// Scheme returns the scheme this builder is responsible for.
func (b *ResolverBuilder) Scheme() string {
	return "registry"
}

// grpcResolver implements resolver.Resolver and contains the logic for service discovery via a registry.
type grpcResolver struct {
	target     resolver.Target
	registry   registry.Registry
	clientConn resolver.ClientConn
	timeout    time.Duration
	closeCh    chan struct{}
}

// ResolveNow attempts to resolve the target again.
func (g *grpcResolver) ResolveNow(_ resolver.ResolveNowOptions) {
	g.resolve()
}

// watch monitors the service discoveries and updates them if necessary.
func (g *grpcResolver) watch() {
	ch, err := g.registry.Subscribe(g.target.Endpoint())
	if err != nil {
		g.clientConn.ReportError(err)
		return
	}

	for {
		select {
		case <-ch:
			g.resolve()
		case <-g.closeCh:
			return
		}
	}
}

// resolve makes an immediate resolution attempt and updates the client's state with addresses.
func (g *grpcResolver) resolve() {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	services, err := g.registry.ListServices(ctx, g.target.Endpoint())
	if err != nil {
		g.clientConn.ReportError(err)
		return
	}

	addresses := make([]resolver.Address, len(services))
	for i, service := range services {
		addresses[i] = resolver.Address{
			Addr: service.Address,
			Attributes: attributes.New("weight", service.Weight).
				WithValue("group", service.Group),
		}
	}

	if err = g.clientConn.UpdateState(resolver.State{Addresses: addresses}); err != nil {
		g.clientConn.ReportError(err)
	}
}

// Close terminates the watching for service discovery updates.
func (g *grpcResolver) Close() {
	close(g.closeCh)
}
