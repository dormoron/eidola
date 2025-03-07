package eidola

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

type ServerOption func(s *Server)

// Server represents a gRPC server
type Server struct {
	name            string
	registry        registry.Registry
	registerTimeout time.Duration
	*grpc.Server
	listener              net.Listener
	weight                uint32
	group                 string
	shutdownTimeout       time.Duration
	maxConnAge            time.Duration
	maxConnIdle           time.Duration
	gracefulStop          bool
	tls                   credentials.TransportCredentials
	maxConcurrentStreams  uint32
	initialWindowSize     int32
	initialConnWindowSize int32
	mu                    sync.RWMutex // 保护共享资源访问
	running               bool
}

// NewServer intializes a new server
func NewServer(name string, opts ...ServerOption) (*Server, error) {
	s := &Server{
		name:                  name,
		registerTimeout:       time.Second * 10,
		shutdownTimeout:       time.Second * 30,
		maxConnAge:            time.Hour,
		maxConnIdle:           time.Minute * 5,
		gracefulStop:          true,
		maxConcurrentStreams:  100,
		initialWindowSize:     1024 * 1024,      // 1MB
		initialConnWindowSize: 1024 * 1024 * 10, // 10MB
	}

	for _, opt := range opts {
		opt(s)
	}

	// 配置默认的服务器选项
	serverOpts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     s.maxConnIdle,
			MaxConnectionAge:      s.maxConnAge,
			MaxConnectionAgeGrace: time.Second * 5,
			Time:                  time.Second * 60,
			Timeout:               time.Second * 10,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             time.Second * 5,
			PermitWithoutStream: true,
		}),
		grpc.MaxConcurrentStreams(s.maxConcurrentStreams),
		grpc.InitialWindowSize(s.initialWindowSize),
		grpc.InitialConnWindowSize(s.initialConnWindowSize),
		grpc.MaxRecvMsgSize(1024 * 1024 * 4), // 4MB
		grpc.MaxSendMsgSize(1024 * 1024 * 4), // 4MB
	}

	// 添加TLS支持
	if s.tls != nil {
		serverOpts = append(serverOpts, grpc.Creds(s.tls))
	}

	s.Server = grpc.NewServer(serverOpts...)

	// 注册健康检查服务
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s.Server, healthServer)

	// 注册反射服务，方便调试
	reflection.Register(s.Server)

	return s, nil
}

// Start starts the server
func (s *Server) Start(addr string) (err error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errs.ErrServerAlreadyRunning()
	}
	s.running = true
	s.mu.Unlock()

	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return errs.ErrServerListening(err)
	}

	// Ensure listener is closed on an error to prevent a leak.
	defer func() {
		if err != nil {
			s.listener.Close()
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}
	}()

	if s.registry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.registerTimeout)
		defer cancel()
		err = s.registry.Register(ctx, registry.ServiceInstance{
			Name:    s.name,
			Address: s.listener.Addr().String(),
			Weight:  s.weight,
			Group:   s.group,
		})
		if err != nil {
			return errs.ErrServerRegister(err)
		}
	}

	// 设置信号处理器，优雅关闭
	if s.gracefulStop {
		go s.handleSignals()
	}

	// 输出服务启动信息
	connType := "insecure"
	if s.tls != nil {
		connType = "secure"
	}

	// 打印服务启动信息到标准输出，实际使用时可以替换为结构化日志
	fmt.Printf("gRPC %s server '%s' is listening on %s\n",
		connType, s.name, s.listener.Addr().String())

	err = s.Serve(s.listener)
	if err != nil {
		return errs.ErrFailedServe(err)
	}

	return nil
}

// handleSignals 处理系统信号，实现优雅关闭
func (s *Server) handleSignals() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// 接收到信号
	receivedSignal := <-sigs

	// 记录接收到的信号
	fmt.Printf("Received signal %v for server '%s', initiating graceful shutdown\n",
		receivedSignal, s.name)

	// 创建一个计时器，在超时后强制关闭
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()

	// 等待优雅关闭或超时
	select {
	case <-done:
		// 优雅关闭完成
		fmt.Printf("Server '%s' gracefully stopped\n", s.name)
	case <-time.After(s.shutdownTimeout):
		// 超时，强制关闭
		fmt.Printf("Graceful shutdown of server '%s' timed out after %v\n",
			s.name, s.shutdownTimeout)
		s.Server.Stop()
	}
}

// Close gracefully stops the server
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.registry != nil {
		// 尝试从注册中心注销服务
		ctx, cancel := context.WithTimeout(context.Background(), s.registerTimeout)
		defer cancel()

		if err := s.registry.UnRegister(ctx, registry.ServiceInstance{
			Name:    s.name,
			Address: s.listener.Addr().String(),
		}); err != nil {
			// 注销失败仍然继续关闭服务
			fmt.Printf("Failed to deregister service '%s': %v\n", s.name, err)
		}

		// 关闭注册中心连接
		if err := s.registry.Close(); err != nil {
			return errs.ErrServerClose(err)
		}
	}

	// 优雅停止服务器
	stopped := make(chan struct{})
	go func() {
		s.GracefulStop()
		close(stopped)
	}()

	// 等待优雅关闭完成或超时
	select {
	case <-stopped:
		// 优雅关闭成功
	case <-time.After(s.shutdownTimeout):
		// 超时，强制关闭
		s.Server.Stop()
	}

	s.running = false
	return nil
}

// IsRunning 检查服务器是否正在运行
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// ServerWithRegistry sets the registry for the server
func ServerWithRegistry(reg registry.Registry) ServerOption {
	return func(s *Server) {
		s.registry = reg
	}
}

// ServerWithRegisterTimeout sets the register timeout for the server
func ServerWithRegisterTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		s.registerTimeout = d
	}
}

// ServerWithWeight sets the weight for the server
func ServerWithWeight(weight uint32) ServerOption {
	return func(s *Server) {
		s.weight = weight
	}
}

// ServerWithGroup sets the group for the server
func ServerWithGroup(group string) ServerOption {
	return func(s *Server) {
		s.group = group
	}
}

// ServerWithTLS 设置服务器TLS证书
func ServerWithTLS(creds credentials.TransportCredentials) ServerOption {
	return func(s *Server) {
		s.tls = creds
	}
}

// ServerWithGracefulStop 设置是否使用优雅关闭
func ServerWithGracefulStop(graceful bool) ServerOption {
	return func(s *Server) {
		s.gracefulStop = graceful
	}
}

// ServerWithShutdownTimeout 设置关闭超时时间
func ServerWithShutdownTimeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.shutdownTimeout = timeout
	}
}

// ServerWithConnectionAge 设置连接最大年龄
func ServerWithConnectionAge(maxAge time.Duration) ServerOption {
	return func(s *Server) {
		s.maxConnAge = maxAge
	}
}

// ServerWithConnectionIdle 设置连接空闲超时
func ServerWithConnectionIdle(maxIdle time.Duration) ServerOption {
	return func(s *Server) {
		s.maxConnIdle = maxIdle
	}
}

// ServerWithStreamConfig 设置流配置
func ServerWithStreamConfig(maxStreams uint32, windowSize, connWindowSize int32) ServerOption {
	return func(s *Server) {
		s.maxConcurrentStreams = maxStreams
		s.initialWindowSize = windowSize
		s.initialConnWindowSize = connWindowSize
	}
}
