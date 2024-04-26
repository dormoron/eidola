package random

import (
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"math/rand"
)

type Balancer struct {
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
	idx := rand.Intn(len(candidates))
	return balancer.PickResult{
		SubConn: candidates[idx].c,
		Done: func(info balancer.DoneInfo) {

		},
	}, nil

}

type BalancerBuilder struct {
	Filter loadbalance.Filter
}

func (b *BalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]subConn, 0, len(info.ReadySCs))
	for c, ci := range info.ReadySCs {
		connections = append(connections, subConn{c: c, addr: ci.Address})
	}
	res := &Balancer{
		connections: connections,
		filter:      b.Filter,
	}
	return res
}

type subConn struct {
	c    balancer.SubConn
	addr resolver.Address
}