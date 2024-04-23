package leastactive

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"math"
	"sync/atomic"
)

type Balancer struct {
	connections []*activeConn
}

func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	res := &activeConn{}
	res.cnt.Store(math.MaxUint32)
	for _, c := range b.connections {
		if c.cnt.Load() <= res.cnt.Load() {
			res = c
		}
	}
	res.cnt.Store(res.cnt.Load() + 1)
	return balancer.PickResult{
		SubConn: res.c,
		Done: func(info balancer.DoneInfo) {
			res.cnt.Store(res.cnt.Load() - 1)
		},
	}, nil
}

type BalancerBuilder struct {
}

func (b *BalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]*activeConn, 0, len(info.ReadySCs))
	for c := range info.ReadySCs {
		connections = append(connections, &activeConn{
			c: c,
		})
	}
	return &Balancer{connections: connections}
}

type activeConn struct {
	cnt atomic.Uint32
	c   balancer.SubConn
}
