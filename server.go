package eidola

import (
	"context"
	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/registry"
	"google.golang.org/grpc"
	"net"
	"time"
)

type ServerOption func(s *Server)

// Server represents a gRPC server
type Server struct {
	name            string
	registry        registry.Registry
	registerTimeout time.Duration
	*grpc.Server
	listener net.Listener
	weight   uint32
	group    string
}

// NewServer intializes a new server
func NewServer(name string, opts ...ServerOption) (*Server, error) {
	s := &Server{
		name:            name,
		Server:          grpc.NewServer(),
		registerTimeout: time.Second * 10,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Start starts the server
func (s *Server) Start(addr string) (err error) {
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return errs.ErrServerListening(err)
	}

	// Ensure listener is closed on an error to prevent a leak.
	defer func() {
		if err != nil {
			s.listener.Close()
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

	err = s.Serve(s.listener)
	if err != nil {
		return errs.ErrFailedServe(err)
	}

	return nil
}

// Close gracefully stops the server
func (s *Server) Close() error {
	if s.registry != nil {
		err := s.registry.Close()
		if err != nil {
			return errs.ErrServerClose(err)
		}
	}
	s.GracefulStop()
	return nil
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
