package random

import (
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"math/rand"
	"sync"
	"time"
)

type weightConn struct {
	c      balancer.SubConn
	weight int
	addr   resolver.Address
}

// WeightBalancer is a load balancer that considers the weight of each connection.
type WeightBalancer struct {
	connections []*weightConn
	filter      loadbalance.Filter
	rand        *rand.Rand // Random number generator.
	mutex       sync.Mutex // Mutex to protect the random number generator from concurrent access.
}

// Pick selects a sub-connection based on its weight.
func (b *WeightBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	var totalWeight int
	candidates := make([]*weightConn, 0, len(b.connections))

	// Filter connections and sum their weights.
	for _, c := range b.connections {
		if b.filter != nil && !b.filter(info, c.addr) {
			continue
		}
		totalWeight += c.weight
		candidates = append(candidates, c)
	}

	// Check if there are any candidates.
	if len(candidates) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// Randomly select a connection based on weight.
	tgt := b.rand.Intn(totalWeight)
	for _, c := range candidates {
		if tgt < c.weight {
			return balancer.PickResult{
				SubConn: c.c,
				Done:    func(balancer.DoneInfo) {},
			}, nil
		}
		tgt -= c.weight
	}

	// Fallback return.
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

// WeightBalancerBuilder builds a WeightBalancer.
type WeightBalancerBuilder struct {
	Filter loadbalance.Filter
}

// Build constructs a new WeightBalancer from PickerBuildInfo.
func (b *WeightBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	var totalWeight int
	cs := make([]*weightConn, 0, len(info.ReadySCs))

	// Create connections and calculate their total weight.
	for sub, subInfo := range info.ReadySCs {
		weight, _ := subInfo.Address.Attributes.Value("weight").(int) // Safe type assertion; default to zero if not set.
		totalWeight += weight
		cs = append(cs, &weightConn{
			c:      sub,
			weight: weight,
			addr:   subInfo.Address,
		})
	}

	// Initialize the random number generator with a mutex.
	src := rand.NewSource(time.Now().UnixNano())
	randGen := rand.New(src)

	return &WeightBalancer{
		connections: cs,
		filter:      b.Filter,
		rand:        randGen,
	}
}
