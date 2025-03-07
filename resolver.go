package eidola

import (
	"context"
	"sync"
	"time"

	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc/attributes"
	"google.golang.org/grpc/resolver"
)

// ResolverOptions defines a functional option for configuring a ResolverBuilder.
type ResolverOptions func(r *ResolverBuilder)

// ResolverBuilder constructs a grpcResolver with registry and timeout settings.
type ResolverBuilder struct {
	registry      registry.Registry
	timeout       time.Duration
	refreshPeriod time.Duration
}

// NewResolverBuilder creates a new ResolverBuilder and applies any additional options.
func NewResolverBuilder(registry registry.Registry, opts ...ResolverOptions) (*ResolverBuilder, error) {
	builder := &ResolverBuilder{
		registry:      registry,
		timeout:       3 * time.Second,  // Default timeout set to 3 seconds.
		refreshPeriod: 30 * time.Second, // 默认30秒刷新一次服务列表
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

// ResolverWithRefreshPeriod 设置服务列表定期刷新间隔
func ResolverWithRefreshPeriod(period time.Duration) ResolverOptions {
	return func(builder *ResolverBuilder) {
		builder.refreshPeriod = period
	}
}

// Build constructs a grpcResolver for a given target and client connection with additional resolver build options.
func (b *ResolverBuilder) Build(target resolver.Target, clientConn resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &grpcResolver{
		target:        target,
		registry:      b.registry,
		clientConn:    clientConn,
		timeout:       b.timeout,
		refreshPeriod: b.refreshPeriod,
		closeCh:       make(chan struct{}),
		wg:            &sync.WaitGroup{},
		lastServices:  make(map[string]struct{}),
	}

	// Start initial resolution and the monitoring goroutine.
	if err := r.resolve(); err != nil {
		return nil, err
	}

	r.wg.Add(1)
	go r.watch()

	// 启动定期刷新
	if b.refreshPeriod > 0 {
		r.wg.Add(1)
		go r.periodicRefresh()
	}

	return r, nil
}

// Scheme returns the scheme this builder is responsible for.
func (b *ResolverBuilder) Scheme() string {
	return "registry"
}

// grpcResolver implements resolver.Resolver and contains the logic for service discovery via a registry.
type grpcResolver struct {
	target        resolver.Target
	registry      registry.Registry
	clientConn    resolver.ClientConn
	timeout       time.Duration
	refreshPeriod time.Duration
	closeCh       chan struct{}
	wg            *sync.WaitGroup
	lastServices  map[string]struct{} // 记录上次解析到的服务地址，用于检测变化
	mu            sync.RWMutex        // 保护lastServices
}

// ResolveNow attempts to resolve the target again.
func (g *grpcResolver) ResolveNow(_ resolver.ResolveNowOptions) {
	_ = g.resolve() // 忽略错误，因为ResolveNow不应该返回错误
}

// periodicRefresh 定期刷新服务列表
func (g *grpcResolver) periodicRefresh() {
	defer g.wg.Done()
	ticker := time.NewTicker(g.refreshPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = g.resolve()
		case <-g.closeCh:
			return
		}
	}
}

// watch monitors the service discoveries and updates them if necessary.
func (g *grpcResolver) watch() {
	defer g.wg.Done()

	// 启动订阅
	ch, err := g.registry.Subscribe(g.target.Endpoint())
	if err != nil {
		g.clientConn.ReportError(err)
		return
	}

	for {
		select {
		case <-ch:
			if err := g.resolve(); err != nil {
				g.clientConn.ReportError(err)
			}
		case <-g.closeCh:
			return
		}
	}
}

// resolve makes an immediate resolution attempt and updates the client's state with addresses.
func (g *grpcResolver) resolve() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	services, err := g.registry.ListServices(ctx, g.target.Endpoint())
	if err != nil {
		g.clientConn.ReportError(err)
		return err
	}

	// 检查是否有变化
	changed := g.hasServiceChanged(services)
	if !changed && len(services) > 0 {
		return nil // 如果没有变化且有地址，则不更新
	}

	if len(services) == 0 {
		// 如果没有找到服务，还是报告一下但不返回错误
		// 这样可以防止服务暂时不可用时客户端断开连接
		g.clientConn.ReportError(
			&noServiceError{service: g.target.Endpoint()})
	}

	addresses := make([]resolver.Address, len(services))
	for i, service := range services {
		addresses[i] = resolver.Address{
			Addr: service.Address,
			Attributes: attributes.New("weight", service.Weight).
				WithValue("group", service.Group),
		}
	}

	err = g.clientConn.UpdateState(resolver.State{Addresses: addresses})
	if err != nil {
		g.clientConn.ReportError(err)
		return err
	}

	// 更新记录的服务地址
	g.recordServices(services)

	return nil
}

// hasServiceChanged 检查服务列表是否有变化
func (g *grpcResolver) hasServiceChanged(services []registry.ServiceInstance) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(services) != len(g.lastServices) {
		return true
	}

	for _, svc := range services {
		if _, ok := g.lastServices[svc.Address]; !ok {
			return true
		}
	}

	return false
}

// recordServices 记录当前解析到的服务地址
func (g *grpcResolver) recordServices(services []registry.ServiceInstance) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.lastServices = make(map[string]struct{})
	for _, svc := range services {
		g.lastServices[svc.Address] = struct{}{}
	}
}

// Close terminates the watching for service discovery updates.
func (g *grpcResolver) Close() {
	close(g.closeCh)
	g.wg.Wait() // 等待所有goroutine退出
}

// noServiceError 表示找不到服务的错误
type noServiceError struct {
	service string
}

func (e *noServiceError) Error() string {
	return "no available instances for service: " + e.service
}
