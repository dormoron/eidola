package hash

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"hash/fnv"
)

type Balancer struct {
	connections []balancer.SubConn
	length      int
}

func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if b.length == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	hashed := fnv.New32a()
	_, err := hashed.Write([]byte(info.FullMethodName))
	if err != nil {
		return balancer.PickResult{}, err
	}
	hash := hashed.Sum32()

	choice := int(hash) % b.length

	return balancer.PickResult{
		SubConn: b.connections[choice],
		Done: func(info balancer.DoneInfo) {

		},
	}, nil
}

type BalancerBuilder struct{}

func (b *BalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	connections := make([]balancer.SubConn, 0, len(info.ReadySCs))
	for subConn := range info.ReadySCs {
		connections = append(connections, subConn)
	}
	return &Balancer{
		connections: connections,
		length:      len(connections),
	}
}
