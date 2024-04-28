package round_robin

import (
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"sync/atomic"
)

// Balancer manages load balancing over a set of connections with filtering capabilities.
type Balancer struct {
	index       atomic.Int32       // Maintains round-robin index safely across goroutines
	connections []subConn          // Slice of sub connections available for balancing
	filter      loadbalance.Filter // Optional filter to apply during picking connections
}

// Pick selects the next subConn based on the filter criteria and using round-robin.
func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// creating a slice to store potential connections for selection
	candidates := make([]subConn, 0, len(b.connections))
	for _, conn := range b.connections {
		if b.filter == nil || b.filter(info, conn.addr) {
			candidates = append(candidates, conn)
		}
	}
	if len(candidates) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// Round-robin selection of sub connections
	index := b.index.Add(1)
	selected := candidates[index%int32(len(candidates))]
	return balancer.PickResult{
		SubConn: selected.conn,
		Done:    func(balancer.DoneInfo) {},
	}, nil
}

// Builder constructs a Balancer with a provided filter.
type Builder struct {
	Filter loadbalance.Filter // Filter is to apply during the balancing process
}

// Build constructs an instance of Balancer from the provided PickerBuildInfo.
func (b *Builder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]subConn, 0, len(info.ReadySCs))
	for conn, connInfo := range info.ReadySCs {
		connections = append(connections, subConn{
			conn: conn,
			addr: connInfo.Address,
		})
	}

	res := &Balancer{
		connections: connections,
		filter:      b.Filter,
	}
	res.index.Store(-1)
	return res
}

// subConn stores the data related to each sub connection.
type subConn struct {
	conn balancer.SubConn // The gRPC sub-connection object
	addr resolver.Address // The address information of the sub-connection
}
