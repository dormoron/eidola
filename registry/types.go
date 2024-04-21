package registry

import "context"

type Registry interface {
	Registry(ctx context.Context, si ServiceInstance) error
	UnRegistry(ctx context.Context, si ServiceInstance) error
	ListService(ctx context.Context, name string) ([]ServiceInstance, error)
	Subscribe(name string) (<-chan []Event, error)
	Close() error
}

type ServiceInstance struct {
	Name    string
	Address string
}

type Event struct {
	Type string
}
