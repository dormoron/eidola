package round_robin

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"math"
	"sync"
	"sync/atomic"
)

type WeightBalancer struct {
	connections []*weightConn
	mutex       sync.Mutex
}

func (w *WeightBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if len(w.connections) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	var totalWeight uint32
	var res *weightConn
	w.mutex.Lock()
	defer w.mutex.Unlock()
	for _, c := range w.connections {
		totalWeight = totalWeight + c.efficientWeight
		c.currentWeight = c.currentWeight + c.efficientWeight
		if res == nil || res.currentWeight < c.currentWeight {
			res = c
		}
	}
	res.currentWeight = res.currentWeight - totalWeight
	return balancer.PickResult{
		SubConn: res.c,
		Done: func(info balancer.DoneInfo) {
			for {
				weight := atomic.LoadUint32(&res.efficientWeight)
				if info.Err != nil && weight == 0 {
					return
				}
				if info.Err == nil && weight == math.MaxUint32 {
					return
				}
				newWeight := weight
				if info.Err != nil {
					newWeight--
				} else {
					newWeight++
				}
				if atomic.CompareAndSwapUint32(&(res.efficientWeight), weight, newWeight) {
					return
				}
			}
		},
	}, nil
}

type WeightBalancerBuilder struct {
}

func (w *WeightBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	cs := make([]*weightConn, 0, len(info.ReadySCs))
	for sub, subInfo := range info.ReadySCs {
		weight := subInfo.Address.Attributes.Value("weight").(uint32)
		cs = append(cs, &weightConn{
			c:               sub,
			weight:          weight,
			currentWeight:   weight,
			efficientWeight: weight,
		})
	}
	return &WeightBalancer{
		connections: cs,
	}
}

type weightConn struct {
	c               balancer.SubConn
	weight          uint32
	currentWeight   uint32
	efficientWeight uint32
}
