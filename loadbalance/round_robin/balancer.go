package round_robin

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"sync/atomic"
)

type Balancer struct {
	index       atomic.Int32
	connections []balancer.SubConn
	length      int32
}

func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.index.Store((b.index.Load() + 1) % b.length)
	c := b.connections[b.index.Load()]
	return balancer.PickResult{
		SubConn: c,
		Done: func(info balancer.DoneInfo) {
			// todo
		},
	}, nil
}

type Builder struct {
}

func (b Builder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]balancer.SubConn, 0, len(info.ReadySCs))
	for c := range info.ReadySCs {
		connections = append(connections, c)
	}
	res := &Balancer{
		connections: connections,
		length:      int32(len(info.ReadySCs)),
	}
	res.index.Store(-1)
	return res
}
