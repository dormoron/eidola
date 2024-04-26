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

type Registry struct {
	client  *clientv3.Client
	session *concurrency.Session
	cancels []func()
	mutex   sync.Mutex
}

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

func (r *Registry) Register(ctx context.Context, si registry.ServiceInstance) error {
	val, err := json.Marshal(si)
	if err != nil {
		return err
	}
	_, err = r.client.Put(ctx, r.instanceKey(si), string(val), clientv3.WithLease(r.session.Lease()))
	return err
}

func (r *Registry) UnRegister(ctx context.Context, si registry.ServiceInstance) error {
	_, err := r.client.Delete(ctx, r.instanceKey(si))
	return err
}

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

func (r *Registry) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	for _, cancel := range r.cancels {
		cancel()
	}
	r.cancels = nil
	return r.session.Close()
}

func (r *Registry) instanceKey(si registry.ServiceInstance) string {
	return fmt.Sprintf("/%s/%s", si.Name, si.Address)
}

func (r *Registry) serviceKey(name string) string {
	return fmt.Sprintf("/%s", name)
}
