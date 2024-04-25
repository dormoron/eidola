package round_robin

import (
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"sync/atomic"
)

type Balancer struct {
	index       atomic.Int32
	connections []subConn
	filter      loadbalance.Filter
}

func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	candidates := make([]subConn, 0, len(b.connections))
	for _, c := range b.connections {
		if b.filter != nil && !b.filter(info, c.addr) {
			continue
		}
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	b.index.Store(b.index.Load() + 1)
	c := candidates[b.index.Load()%int32(len(candidates))]
	return balancer.PickResult{
		SubConn: c.c,
		Done:    func(info balancer.DoneInfo) {},
	}, nil
}

type Builder struct {
	Filter loadbalance.Filter
}

func (b Builder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]subConn, 0, len(info.ReadySCs))
	for c, ci := range info.ReadySCs {
		connections = append(connections, subConn{
			c:    c,
			addr: ci.Address,
		})
	}
	res := &Balancer{
		connections: connections,
		filter:      b.Filter,
	}
	res.index.Store(-1)
	return res
}

type subConn struct {
	c    balancer.SubConn
	addr resolver.Address
}
