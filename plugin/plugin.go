package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc"
)

// Plugin 定义了插件的基本接口
type Plugin interface {
	// Name 返回插件的名称
	Name() string
	// Init 初始化插件，在服务启动前调用
	Init() error
	// Close 关闭插件，在服务关闭时调用
	Close() error
}

// ServerPlugin 定义了服务端插件的接口
type ServerPlugin interface {
	Plugin
	// RegisterWithServer 将插件注册到gRPC服务器
	RegisterWithServer(*grpc.Server) error
}

// ClientPlugin 定义了客户端插件的接口
type ClientPlugin interface {
	Plugin
	// ModifyClientOptions 允许插件修改客户端选项
	ModifyClientOptions([]grpc.DialOption) []grpc.DialOption
}

// InterceptorPlugin 定义了拦截器插件的接口
type InterceptorPlugin interface {
	Plugin
	// UnaryServerInterceptor 返回一个一元服务端拦截器
	UnaryServerInterceptor() grpc.UnaryServerInterceptor
	// StreamServerInterceptor 返回一个流服务端拦截器
	StreamServerInterceptor() grpc.StreamServerInterceptor
	// UnaryClientInterceptor 返回一个一元客户端拦截器
	UnaryClientInterceptor() grpc.UnaryClientInterceptor
	// StreamClientInterceptor 返回一个流客户端拦截器
	StreamClientInterceptor() grpc.StreamClientInterceptor
}

// Manager 管理所有已注册的插件
type Manager struct {
	plugins      map[string]Plugin
	initOrder    []string // 用于跟踪初始化顺序，以便按照相反的顺序关闭
	mu           sync.RWMutex
	initComplete bool
}

// NewManager 创建一个新的插件管理器
func NewManager() *Manager {
	return &Manager{
		plugins:   make(map[string]Plugin),
		initOrder: make([]string, 0),
	}
}

// Register 注册一个插件
func (m *Manager) Register(p Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initComplete {
		return errors.New("cannot register plugin after initialization")
	}

	name := p.Name()
	if _, exists := m.plugins[name]; exists {
		return fmt.Errorf("plugin %s already registered", name)
	}

	m.plugins[name] = p
	return nil
}

// Get 获取一个已注册的插件
func (m *Manager) Get(name string) (Plugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, exists := m.plugins[name]
	return p, exists
}

// GetAll 获取所有已注册的插件
func (m *Manager) GetAll() []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugins := make([]Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		plugins = append(plugins, p)
	}
	return plugins
}

// GetByType 获取特定类型的所有插件
func (m *Manager) GetByType(pluginType interface{}) []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Plugin
	for _, p := range m.plugins {
		switch pluginType.(type) {
		case ServerPlugin:
			if _, ok := p.(ServerPlugin); ok {
				result = append(result, p)
			}
		case ClientPlugin:
			if _, ok := p.(ClientPlugin); ok {
				result = append(result, p)
			}
		case InterceptorPlugin:
			if _, ok := p.(InterceptorPlugin); ok {
				result = append(result, p)
			}
		}
	}
	return result
}

// Init 初始化所有已注册的插件
func (m *Manager) Init() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initComplete {
		return errors.New("plugins already initialized")
	}

	for name, p := range m.plugins {
		if err := p.Init(); err != nil {
			return fmt.Errorf("failed to initialize plugin %s: %w", name, err)
		}
		m.initOrder = append(m.initOrder, name)
	}

	m.initComplete = true
	return nil
}

// Close 关闭所有已注册的插件
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initComplete {
		return nil // 没有初始化，不需要关闭
	}

	var errs []error
	// 按照初始化的相反顺序关闭插件
	for i := len(m.initOrder) - 1; i >= 0; i-- {
		name := m.initOrder[i]
		if p, exists := m.plugins[name]; exists {
			if err := p.Close(); err != nil {
				errs = append(errs, fmt.Errorf("failed to close plugin %s: %w", name, err))
			}
		}
	}

	m.initComplete = false
	m.initOrder = make([]string, 0)

	if len(errs) > 0 {
		return fmt.Errorf("errors closing plugins: %v", errs)
	}
	return nil
}

// ApplyServerPlugins 将所有服务端插件应用到gRPC服务器
func (m *Manager) ApplyServerPlugins(server *grpc.Server) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initComplete {
		return errors.New("plugins not initialized")
	}

	for name, p := range m.plugins {
		if sp, ok := p.(ServerPlugin); ok {
			if err := sp.RegisterWithServer(server); err != nil {
				return fmt.Errorf("failed to register server plugin %s: %w", name, err)
			}
		}
	}
	return nil
}

// ApplyClientPlugins 将所有客户端插件应用到gRPC客户端选项
func (m *Manager) ApplyClientPlugins(opts []grpc.DialOption) []grpc.DialOption {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initComplete {
		return opts
	}

	result := opts
	for _, p := range m.plugins {
		if cp, ok := p.(ClientPlugin); ok {
			result = cp.ModifyClientOptions(result)
		}
	}
	return result
}

// GetInterceptors 获取所有拦截器插件的拦截器
func (m *Manager) GetInterceptors() (
	unaryServer []grpc.UnaryServerInterceptor,
	streamServer []grpc.StreamServerInterceptor,
	unaryClient []grpc.UnaryClientInterceptor,
	streamClient []grpc.StreamClientInterceptor,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initComplete {
		return
	}

	for _, p := range m.plugins {
		if ip, ok := p.(InterceptorPlugin); ok {
			if interceptor := ip.UnaryServerInterceptor(); interceptor != nil {
				unaryServer = append(unaryServer, interceptor)
			}
			if interceptor := ip.StreamServerInterceptor(); interceptor != nil {
				streamServer = append(streamServer, interceptor)
			}
			if interceptor := ip.UnaryClientInterceptor(); interceptor != nil {
				unaryClient = append(unaryClient, interceptor)
			}
			if interceptor := ip.StreamClientInterceptor(); interceptor != nil {
				streamClient = append(streamClient, interceptor)
			}
		}
	}
	return
}

// LifecycleHook 定义了插件生命周期钩子
type LifecycleHook interface {
	// BeforeStart 在服务启动前调用
	BeforeStart(context.Context) error
	// AfterStart 在服务启动后调用
	AfterStart(context.Context) error
	// BeforeStop 在服务停止前调用
	BeforeStop(context.Context) error
	// AfterStop 在服务停止后调用
	AfterStop(context.Context) error
}

// LifecycleManager 管理插件的生命周期事件
type LifecycleManager struct {
	manager *Manager
}

// NewLifecycleManager 创建一个新的生命周期管理器
func NewLifecycleManager(manager *Manager) *LifecycleManager {
	return &LifecycleManager{
		manager: manager,
	}
}

// ExecuteBeforeStart 执行所有插件的BeforeStart钩子
func (lm *LifecycleManager) ExecuteBeforeStart(ctx context.Context) error {
	for _, p := range lm.manager.GetAll() {
		if hook, ok := p.(LifecycleHook); ok {
			if err := hook.BeforeStart(ctx); err != nil {
				return fmt.Errorf("plugin %s BeforeStart hook failed: %w", p.Name(), err)
			}
		}
	}
	return nil
}

// ExecuteAfterStart 执行所有插件的AfterStart钩子
func (lm *LifecycleManager) ExecuteAfterStart(ctx context.Context) error {
	for _, p := range lm.manager.GetAll() {
		if hook, ok := p.(LifecycleHook); ok {
			if err := hook.AfterStart(ctx); err != nil {
				return fmt.Errorf("plugin %s AfterStart hook failed: %w", p.Name(), err)
			}
		}
	}
	return nil
}

// ExecuteBeforeStop 执行所有插件的BeforeStop钩子
func (lm *LifecycleManager) ExecuteBeforeStop(ctx context.Context) error {
	// 按照初始化的相反顺序执行
	plugins := lm.manager.GetAll()
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		if hook, ok := p.(LifecycleHook); ok {
			if err := hook.BeforeStop(ctx); err != nil {
				return fmt.Errorf("plugin %s BeforeStop hook failed: %w", p.Name(), err)
			}
		}
	}
	return nil
}

// ExecuteAfterStop 执行所有插件的AfterStop钩子
func (lm *LifecycleManager) ExecuteAfterStop(ctx context.Context) error {
	// 按照初始化的相反顺序执行
	plugins := lm.manager.GetAll()
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		if hook, ok := p.(LifecycleHook); ok {
			if err := hook.AfterStop(ctx); err != nil {
				return fmt.Errorf("plugin %s AfterStop hook failed: %w", p.Name(), err)
			}
		}
	}
	return nil
}
