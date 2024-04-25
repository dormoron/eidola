package random

import (
	"eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"math/rand"
)

type WeightBalancer struct {
	connections []*weightConn
	filter      loadbalance.Filter
}

func (b *WeightBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	var totalWeight int
	candidates := make([]*weightConn, 0, len(b.connections))
	tgt := rand.Intn(totalWeight + 1)
	for _, c := range b.connections {
		if b.filter != nil && !b.filter(info, c.addr) {
			continue
		}
		candidates = append(candidates, c)
		totalWeight += c.weight
	}
	if len(candidates) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	var idx int
	for i, c := range candidates {
		tgt = tgt - c.weight
		if tgt <= 0 {
			idx = i
			break
		}
	}
	return balancer.PickResult{
		SubConn: candidates[idx].c,
		Done: func(info balancer.DoneInfo) {
		},
	}, nil
}

type WeightBalancerBuilder struct {
	Filter loadbalance.Filter
}

func (b *WeightBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	cs := make([]*weightConn, 0, len(info.ReadySCs))
	var totalWeight int
	for sub, subInfo := range info.ReadySCs {
		weight := subInfo.Address.Attributes.Value("weight").(int)
		totalWeight += weight
		cs = append(cs, &weightConn{
			c:      sub,
			weight: weight,
			addr:   subInfo.Address,
		})
	}
	return &WeightBalancer{
		connections: cs,
		filter:      b.Filter,
	}
}

type weightConn struct {
	c      balancer.SubConn
	weight int
	addr   resolver.Address
}
