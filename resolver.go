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
	registry         registry.Registry
	timeout          time.Duration
	refreshPeriod    time.Duration
	retryAttempts    int           // 重试次数
	retryDelay       time.Duration // 重试延迟
	cacheTTL         time.Duration // 缓存有效期
	fallbackResolver string        // 后备解析器
	fallbackAddrs    []string      // 后备地址列表
}

// NewResolverBuilder creates a new ResolverBuilder and applies any additional options.
func NewResolverBuilder(registry registry.Registry, opts ...ResolverOptions) (*ResolverBuilder, error) {
	builder := &ResolverBuilder{
		registry:      registry,
		timeout:       3 * time.Second,        // Default timeout set to 3 seconds.
		refreshPeriod: 30 * time.Second,       // 默认30秒刷新一次服务列表
		retryAttempts: 3,                      // 默认重试3次
		retryDelay:    500 * time.Millisecond, // 默认重试延迟500ms
		cacheTTL:      5 * time.Minute,        // 默认缓存5分钟
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

// ResolverWithRetry 设置重试选项
func ResolverWithRetry(attempts int, delay time.Duration) ResolverOptions {
	return func(builder *ResolverBuilder) {
		builder.retryAttempts = attempts
		builder.retryDelay = delay
	}
}

// ResolverWithCache 设置缓存选项
func ResolverWithCache(ttl time.Duration) ResolverOptions {
	return func(builder *ResolverBuilder) {
		builder.cacheTTL = ttl
	}
}

// ResolverWithFallback 设置后备解析机制
func ResolverWithFallback(resolverType string, addrs ...string) ResolverOptions {
	return func(builder *ResolverBuilder) {
		builder.fallbackResolver = resolverType
		builder.fallbackAddrs = addrs
	}
}

// Build constructs a grpcResolver for a given target and client connection with additional resolver build options.
func (b *ResolverBuilder) Build(target resolver.Target, clientConn resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &grpcResolver{
		target:           target,
		registry:         b.registry,
		clientConn:       clientConn,
		timeout:          b.timeout,
		refreshPeriod:    b.refreshPeriod,
		retryAttempts:    b.retryAttempts,
		retryDelay:       b.retryDelay,
		cacheTTL:         b.cacheTTL,
		fallbackResolver: b.fallbackResolver,
		fallbackAddrs:    b.fallbackAddrs,
		closeCh:          make(chan struct{}),
		wg:               &sync.WaitGroup{},
		lastServices:     make(map[string]struct{}),
		cache:            &resolverCache{},
	}

	// Start initial resolution and the monitoring goroutine.
	if err := r.resolve(); err != nil {
		// 尝试使用后备方案
		if len(r.fallbackAddrs) > 0 {
			addresses := make([]resolver.Address, len(r.fallbackAddrs))
			for i, addr := range r.fallbackAddrs {
				addresses[i] = resolver.Address{
					Addr: addr,
					Attributes: attributes.New("weight", uint32(1)).
						WithValue("group", "fallback"),
				}
			}

			r.clientConn.UpdateState(resolver.State{Addresses: addresses})
		} else {
			// 没有后备地址，返回错误
			return nil, err
		}
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

// 解析器缓存
type resolverCache struct {
	services  []registry.ServiceInstance
	timestamp time.Time
	mu        sync.RWMutex
}

// 获取缓存
func (c *resolverCache) get() ([]registry.ServiceInstance, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.services) == 0 || c.timestamp.IsZero() {
		return nil, false
	}

	return c.services, true
}

// 设置缓存
func (c *resolverCache) set(services []registry.ServiceInstance) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.services = services
	c.timestamp = time.Now()
}

// 判断缓存是否有效
func (c *resolverCache) isValid(ttl time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.timestamp.IsZero() || len(c.services) == 0 {
		return false
	}

	return time.Since(c.timestamp) < ttl
}

// grpcResolver implements resolver.Resolver and contains the logic for service discovery via a registry.
type grpcResolver struct {
	target           resolver.Target
	registry         registry.Registry
	clientConn       resolver.ClientConn
	timeout          time.Duration
	refreshPeriod    time.Duration
	retryAttempts    int
	retryDelay       time.Duration
	cacheTTL         time.Duration
	fallbackResolver string
	fallbackAddrs    []string
	closeCh          chan struct{}
	wg               *sync.WaitGroup
	lastServices     map[string]struct{} // 记录上次解析到的服务地址，用于检测变化
	mu               sync.RWMutex        // 保护lastServices
	cache            *resolverCache      // 缓存
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
		// 尝试重试订阅
		go g.retrySubscribe()
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

// retrySubscribe 重试订阅服务变更
func (g *grpcResolver) retrySubscribe() {
	for i := 0; i < g.retryAttempts; i++ {
		select {
		case <-g.closeCh:
			return
		case <-time.After(g.retryDelay):
			// 重试订阅
			ch, err := g.registry.Subscribe(g.target.Endpoint())
			if err == nil {
				// 订阅成功，启动监听
				g.wg.Add(1)
				go func() {
					defer g.wg.Done()
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
				}()
				return
			}
		}
	}

	// 所有重试失败，报告错误
	g.clientConn.ReportError(
		&retryFailedError{service: g.target.Endpoint()})
}

// resolve makes an immediate resolution attempt and updates the client's state with addresses.
func (g *grpcResolver) resolve() error {
	// 先检查缓存是否有效
	if g.cache.isValid(g.cacheTTL) {
		cachedServices, ok := g.cache.get()
		if ok {
			// 检查是否有变化
			if !g.hasServiceChanged(cachedServices) {
				return nil // 使用缓存，无变化
			}

			// 有变化，更新状态
			addresses := make([]resolver.Address, len(cachedServices))
			for i, service := range cachedServices {
				addresses[i] = resolver.Address{
					Addr: service.Address,
					Attributes: attributes.New("weight", service.Weight).
						WithValue("group", service.Group),
				}
			}

			if err := g.clientConn.UpdateState(resolver.State{Addresses: addresses}); err != nil {
				g.clientConn.ReportError(err)
				return err
			}

			// 更新记录的服务地址
			g.recordServices(cachedServices)
			return nil
		}
	}

	// 缓存无效或过期，执行实际解析
	var services []registry.ServiceInstance
	var err error

	// 使用重试机制
	for attempt := 0; attempt <= g.retryAttempts; attempt++ {
		if attempt > 0 {
			// 非首次尝试，等待一段时间
			select {
			case <-time.After(g.retryDelay):
				// 继续重试
			case <-g.closeCh:
				return &resolverClosedError{}
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
		services, err = g.registry.ListServices(ctx, g.target.Endpoint())
		cancel()

		if err == nil {
			break // 成功获取服务列表
		}

		// 最后一次尝试也失败
		if attempt == g.retryAttempts {
			g.clientConn.ReportError(err)

			// 尝试使用后备地址
			if len(g.fallbackAddrs) > 0 {
				addresses := make([]resolver.Address, len(g.fallbackAddrs))
				for i, addr := range g.fallbackAddrs {
					addresses[i] = resolver.Address{
						Addr: addr,
						Attributes: attributes.New("weight", uint32(1)).
							WithValue("group", "fallback"),
					}
				}

				return g.clientConn.UpdateState(resolver.State{Addresses: addresses})
			}

			return err
		}
	}

	// 更新缓存
	g.cache.set(services)

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

		// 尝试使用后备地址
		if len(g.fallbackAddrs) > 0 {
			addresses := make([]resolver.Address, len(g.fallbackAddrs))
			for i, addr := range g.fallbackAddrs {
				addresses[i] = resolver.Address{
					Addr: addr,
					Attributes: attributes.New("weight", uint32(1)).
						WithValue("group", "fallback"),
				}
			}

			return g.clientConn.UpdateState(resolver.State{Addresses: addresses})
		}
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

// retryFailedError 表示重试失败的错误
type retryFailedError struct {
	service string
}

func (e *retryFailedError) Error() string {
	return "failed to subscribe service after retries: " + e.service
}

// resolverClosedError 表示解析器已关闭的错误
type resolverClosedError struct{}

func (e *resolverClosedError) Error() string {
	return "resolver has been closed"
}
