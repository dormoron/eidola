package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/dormoron/eidola/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"sync"
)

// Registry is a struct that handles service registration, discovery, and event subscription.
type Registry struct {
	client  *clientv3.Client     // The etcd client
	session *concurrency.Session // The session for the etcd client
	cancels []func()             // The functions to cancel contexts
	mutex   sync.Mutex           // The mutex for concurrent cancellation of contexts
}

// NewRegistry creates a new Registry with the provided etcd Client and Session Options.
func NewRegistry(c *clientv3.Client, opts ...concurrency.SessionOption) (*Registry, error) {
	sess, err := concurrency.NewSession(c, opts...)
	if err != nil {
		return nil, err
	}
	return &Registry{
		client:  c,
		session: sess,
	}, nil
}

// Register registers a new service instance with the service discovery mechanism.
func (r *Registry) Register(ctx context.Context, si registry.ServiceInstance) error {
	val, err := json.Marshal(si)
	if err != nil {
		return err
	}
	_, err = r.client.Put(ctx, r.instanceKey(si), string(val), clientv3.WithLease(r.session.Lease()))
	return err
}

// UnRegister removes an existing service instance from the service discovery mechanism.
func (r *Registry) UnRegister(ctx context.Context, si registry.ServiceInstance) error {
	_, err := r.client.Delete(ctx, r.instanceKey(si))
	return err
}

// ListServices retrieves a list of service instances by name.
func (r *Registry) ListServices(ctx context.Context, name string) ([]registry.ServiceInstance, error) {
	getResp, err := r.client.Get(ctx, r.serviceKey(name), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	services := make([]registry.ServiceInstance, len(getResp.Kvs))
	for i, kv := range getResp.Kvs {
		if err := json.Unmarshal(kv.Value, &services[i]); err != nil {
			return nil, err
		}
	}
	return services, nil
}

// Subscribe returns a channel that emits events when the specified service updates.
func (r *Registry) Subscribe(name string) (<-chan registry.Event, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mutex.Lock()
	r.cancels = append(r.cancels, cancel)
	r.mutex.Unlock()

	ch := make(chan registry.Event)
	go func() {
		defer close(ch)
		watchChan := r.client.Watch(ctx, r.serviceKey(name), clientv3.WithPrefix())
		for resp := range watchChan {
			if resp.Err() != nil {
				continue // Log or handle error.
			}
			if resp.Canceled {
				return
			}
			for range resp.Events {
				// Process the event.
				ch <- registry.Event{} // Fill Event struct accordingly.
			}
		}
	}()
	return ch, nil
}

// Close closes the Registry by cancelling all contexts and closing the session.
func (r *Registry) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	for _, cancel := range r.cancels {
		cancel()
	}
	r.cancels = nil
	return r.session.Close()
}

// instanceKey constructs the instance key for a particular service instance.
func (r *Registry) instanceKey(si registry.ServiceInstance) string {
	return fmt.Sprintf("/%s/%s", si.Name, si.Address)
}

// serviceKey constructs the service key for a particular service.
func (r *Registry) serviceKey(name string) string {
	return fmt.Sprintf("/%s", name)
}
