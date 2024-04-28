package random

import (
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"math/rand"
	"sync"
	"time"
)

// subConn represents a sub-connection with its address details.
type subConn struct {
	c    balancer.SubConn // The gRPC sub-connection.
	addr resolver.Address // The address information of the sub-connection.
}

// Balancer is a custom load balancer that applies filtering to available connections.
type Balancer struct {
	connections []subConn          // All available connections.
	filter      loadbalance.Filter // Filter logic used to filter connections.
	rand        *rand.Rand         // Random number generator.
	mutex       sync.Mutex         // Guards 'rand'.
}

// Pick selects an appropriate sub-connection using the filter logic and randomizer.
func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// Create a new slice to hold filtered connections candidates.
	candidates := make([]subConn, 0, len(b.connections))

	for _, c := range b.connections {
		// If filter is set and the connection doesn't pass it, skip the connection.
		if b.filter != nil && !b.filter(info, c.addr) {
			continue
		}
		// Add the connection to the candidates list.
		candidates = append(candidates, c)
	}

	// Handle the case when no sub-connections are available.
	if len(candidates) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// Guard the access to the 'rand' with the mutex to prevent data race in multithreading environment.
	b.mutex.Lock()
	// Pick a random index from the candidates.
	idx := b.rand.Intn(len(candidates))
	b.mutex.Unlock()

	// Return the selected sub-connection.
	return balancer.PickResult{
		SubConn: candidates[idx].c,
		Done:    func(info balancer.DoneInfo) {},
	}, nil
}

// BalancerBuilder is a factory that creates instances of the Balancer.
type BalancerBuilder struct {
	Filter loadbalance.Filter // Filter logic to be used by the Balancer instances created by this builder.
}

// Build constructs a new Balancer from the information provided.
func (b *BalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	// Create a new slice to hold connections.
	connections := make([]subConn, 0, len(info.ReadySCs))

	for c, ci := range info.ReadySCs {
		// Add each sub-connection to the slice.
		connections = append(connections, subConn{c: c, addr: ci.Address})
	}

	// Generate a new random source.
	src := rand.NewSource(time.Now().UnixNano())

	// Return a new Balancer.
	return &Balancer{
		connections: connections,
		filter:      b.Filter,
		rand:        rand.New(src),
	}
}
