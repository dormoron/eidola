package hash

import (
	"github.com/dormoron/eidola/loadbalance"
	"github.com/serialx/hashring"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
	"sync"
)

// ConsistentHashBalancer uses consistent hashing to distribute load among connected backends.
type ConsistentHashBalancer struct {
	mutex  sync.RWMutex
	ring   *hashring.HashRing
	conns  map[string]*Conn
	filter loadbalance.Filter
}

// Pick selects a server connection using consistent hashing.
func (b *ConsistentHashBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	if b.ring == nil {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// Use consistent hashing to select a server based on the request key.
	key := info.FullMethodName // In real scenarios, adjust the key according to your requirements.
	server, ok := b.ring.GetNode(key)
	if !ok {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	conn, ok := b.conns[server]
	if !ok || (b.filter != nil && !b.filter(info, conn.Address)) {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	return balancer.PickResult{
		SubConn: conn.SubConn,
		Done:    func(balancer.DoneInfo) {},
	}, nil
}

// UpdateState syncs the balancer state with the latest information about backends.
func (b *ConsistentHashBalancer) UpdateState(newState resolver.State) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// Create a new hash ring with the provided addresses.
	nodes := make([]string, 0, len(newState.Addresses))
	b.conns = make(map[string]*Conn)

	for _, addr := range newState.Addresses {
		serverKey := addr.Addr
		nodes = append(nodes, serverKey)
		b.conns[serverKey] = &Conn{Address: addr}
	}

	// Initialize the hash ring with the server keys.
	b.ring = hashring.New(nodes)
}

// HandleSubConnStateChange updates the state of a SubConn.
func (b *ConsistentHashBalancer) HandleSubConnStateChange(sc balancer.SubConn, state connectivity.State) {
	// Handle the state change of the SubConn.
	// Real implementations should update the ring and connection status accordingly.
}

// Close closes the balancer and cleans up resources.
func (b *ConsistentHashBalancer) Close() {
	// Clean up and dispose of the hash ring.
}

// ConsistentHashBalancerBuilder constructs ConsistentHashBalancer instances.
type ConsistentHashBalancerBuilder struct {
	filter loadbalance.Filter
}

// Build builds a new ConsistentHashBalancer with the provided PickerBuildInfo.
func (b *ConsistentHashBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	conns := make(map[string]*Conn)
	for conn, connInfo := range info.ReadySCs {
		addr := connInfo.Address.Addr
		conns[addr] = &Conn{
			SubConn: conn,
			Address: connInfo.Address,
		}
	}

	balancer := &ConsistentHashBalancer{
		conns:  conns,
		filter: b.filter,
	}

	// Initialized the hash ring with the addresses.
	var nodes []string
	for addr := range conns {
		nodes = append(nodes, addr)
	}
	balancer.ring = hashring.New(nodes)

	return balancer
}
