package hash

import (
	"fmt"
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"hash/fnv"
	"sync"
)

// SimpleHashBalancer is a load balancer that makes decision based on a hash value.
type SimpleHashBalancer struct {
	mutex  sync.RWMutex
	conns  []*Conn
	filter loadbalance.Filter
}

// Pick selects a connection using a simple hash function.
func (b *SimpleHashBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	if len(b.conns) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// Generate hash based on some key in PickInfo, depending on your use case.
	// Here we are simply hashing a string representation of the info, but this should be tailored to your needs.
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(fmt.Sprintf("%v", info)))
	hashValue := hasher.Sum32()

	index := hashValue % uint32(len(b.conns))
	selectedConn := b.conns[index]

	if b.filter != nil && !b.filter(info, selectedConn.Address) {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	return balancer.PickResult{
		SubConn: selectedConn.SubConn,
		Done:    func(info balancer.DoneInfo) {},
	}, nil
}

// Conn represents a connection with its address.
type Conn struct {
	SubConn balancer.SubConn
	Address resolver.Address
}

type SimpleHashBalancerBuilder struct {
	Filter loadbalance.Filter
}

func (b *SimpleHashBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	conns := make([]*Conn, 0, len(info.ReadySCs))
	for conn, scInfo := range info.ReadySCs {
		conns = append(conns, &Conn{
			SubConn: conn,
			Address: scInfo.Address,
		})
	}

	return &SimpleHashBalancer{
		conns:  conns,
		filter: b.Filter,
	}
}
