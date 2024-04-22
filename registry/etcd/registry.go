package etcd

import (
	"context"
	"eidola/registry"
	"encoding/json"
	"fmt"
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

func (r *Registry) Registry(ctx context.Context, si registry.ServiceInstance) error {
	val, err := json.Marshal(si)
	if err != nil {
		return err
	}
	_, err = r.client.Put(ctx, r.instanceKey(si), string(val), clientv3.WithLease(r.session.Lease()))
	return err
}

func (r *Registry) UnRegistry(ctx context.Context, si registry.ServiceInstance) error {
	_, err := r.client.Delete(ctx, r.instanceKey(si))
	return err
}

func (r *Registry) ListService(ctx context.Context, name string) ([]registry.ServiceInstance, error) {
	getResp, err := r.client.Get(ctx, r.serviceKey(name), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	res := make([]registry.ServiceInstance, 0, len(getResp.Kvs))
	for _, kv := range getResp.Kvs {
		var si registry.ServiceInstance
		err = json.Unmarshal(kv.Value, &si)
		if err != nil {
			return nil, err
		}
		res = append(res, si)
	}
	return res, nil
}

func (r *Registry) Subscribe(name string) (<-chan registry.Event, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mutex.Lock()
	r.cancels = append(r.cancels, cancel)
	r.mutex.Unlock()
	ctx = clientv3.WithRequireLeader(ctx)
	watchResp := r.client.Watch(ctx, r.serviceKey(name), clientv3.WithPrefix())
	res := make(chan registry.Event)
	go func() {
		for {
			select {
			case resp := <-watchResp:
				if resp.Err() != nil {
					continue
				}
				if resp.Canceled {
					return
				}
				for range resp.Events {
					res <- registry.Event{}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return res, nil
}

func (r *Registry) Close() error {
	r.mutex.Lock()
	cancels := r.cancels
	r.cancels = nil
	r.mutex.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return r.session.Close()
}

func (r *Registry) instanceKey(si registry.ServiceInstance) string {
	return fmt.Sprintf("/%s/%s", si.Name, si.Address)
}

func (r *Registry) serviceKey(name string) string {
	return fmt.Sprintf("/%s", name)
}
