package leastactive

import (
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"math"
	"sync/atomic"
)

type Balancer struct {
	connections []*activeConn
	filter      loadbalance.Filter
}

func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if len(b.connections) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	var leastActive uint32 = math.MaxUint32
	var res *activeConn
	for _, c := range b.connections {
		if !b.filter(info, c.address) {
			continue
		}
		active := atomic.LoadUint32(&c.active)
		if active < leastActive {
			leastActive = active
			res = c
		}
	}

	atomic.AddUint32(&res.active, 1)
	return balancer.PickResult{
		SubConn: res.SubConn,
		Done: func(info balancer.DoneInfo) {
			atomic.AddUint32(&res.active, -1)
		},
	}, nil
}

type BalancerBuilder struct {
	Filter loadbalance.Filter
}

func (b *BalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]*activeConn, 0, len(info.ReadySCs))
	for con, val := range info.ReadySCs {
		connections = append(connections, &activeConn{
			SubConn: con,
			address: val.Address,
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

type activeConn struct {
	active uint32
	balancer.SubConn
	address resolver.Address
}
