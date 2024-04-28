package registry

import (
	"context"
	"io"
)

// Registry is an interface defining methods for a service discovery system.
type Registry interface {
	// Register registers a new service instance with the service discovery mechanism.
	Register(ctx context.Context, si ServiceInstance) error

	// UnRegister removes an existing service instance from the service discovery mechanism.
	UnRegister(ctx context.Context, si ServiceInstance) error

	// ListServices retrieves a list of service instances by name.
	ListServices(ctx context.Context, name string) ([]ServiceInstance, error)

	// Subscribe returns a channel that emits events when the specified service updates.
	Subscribe(name string) (<-chan Event, error)

	// Closer io.Closer is embedded, meaning that the Registry can be closed, releasing any resources associated with it.
	io.Closer
}

// ServiceInstance defines a single instance of a service that can be registered or discovered.
type ServiceInstance struct {
	Name    string // The logical name of the service instance
	Address string // The address (e.g., IP address and port) of the service instance
	Weight  uint32 // The weight of the instance, which could be used for load-balancing purposes
	Group   string // The grouping of the instance, which could be used for routing or sharding
}

// Event struct is left empty in this example but in practice,
// it can be used to encapsulate details about updates to services in the registry.
type Event struct {
	// Details of the event would go here (e.g., event type, associated service instance)
}
