package leastactive

import (
	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"sync/atomic"
)

type activeConn struct {
	active  uint32
	conn    balancer.SubConn
	address resolver.Address
}

// Balancer is a load balancer that picks the least active connection.
type Balancer struct {
	connections []*activeConn
	filter      loadbalance.Filter
}

// Pick selects the least active connection.
func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	var leastActiveConn *activeConn
	var leastActiveCount = ^uint32(0) // starting max possible value

	for _, conn := range b.connections {
		if !b.filter(info, conn.address) {
			continue
		}

		if count := atomic.LoadUint32(&conn.active); count < leastActiveCount {
			leastActiveCount = count
			leastActiveConn = conn
		}
	}

	if leastActiveConn == nil {
		return balancer.PickResult{}, errs.ErrLoadBalanceAvailable()
	}

	atomic.AddUint32(&leastActiveConn.active, 1)

	return balancer.PickResult{
		SubConn: leastActiveConn.conn,
		Done:    func(info balancer.DoneInfo) { atomic.AddUint32(&leastActiveConn.active, ^uint32(0)) },
	}, nil
}

// BalancerBuilder constructs a new Balancer.
type BalancerBuilder struct {
	Filter loadbalance.Filter
}

// Build creates a new Balancer.
func (b *BalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]*activeConn, 0, len(info.ReadySCs))

	for conn, info := range info.ReadySCs {
		connections = append(connections, &activeConn{
			conn:    conn,
			address: info.Address,
		})
	}

	flt := b.Filter
	if flt == nil {
		flt = func(info balancer.PickInfo, address resolver.Address) bool {
			return true
		}
	}

	return &Balancer{
		connections: connections,
		filter:      flt,
	}
}
