package eidola

import (
	"context"
	"eidola/registry"
	"google.golang.org/grpc"
	"net"
	"time"
)

type ServerOption func(s *Server)

type Server struct {
	name            string
	registry        registry.Registry
	registerTimeout time.Duration
	*grpc.Server
	listener net.Listener
}

func NewServer(name string, opts ...ServerOption) (*Server, error) {
	res := &Server{
		name:            name,
		Server:          grpc.NewServer(),
		registerTimeout: time.Second * 10,
	}
	for _, opt := range opts {
		opt(res)
	}
	return res, nil
}

func (s *Server) Start(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = listener
	if s.registry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.registerTimeout)
		defer cancel()
		err = s.registry.Registry(ctx, registry.ServiceInstance{
			Name:    s.name,
			Address: listener.Addr().String(),
		})
		if err != nil {
			return err
		}
	}
	err = s.Serve(listener)
	return err
}

func (s *Server) Close() error {
	if s.registry != nil {
		err := s.registry.Close()
		if err != nil {
			return err
		}
	}
	s.GracefulStop()
	return nil
}

func ServerWithRegistry(reg registry.Registry) ServerOption {
	return func(s *Server) {
		s.registry = reg
	}
}

func ServerWithRegisterTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		s.registerTimeout = d
	}
}
