package hash

import (
	"fmt"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"hash/fnv"
	"sort"
	"sync"
)

type hashRing []uint32

func (h hashRing) Len() int {
	return len(h)
}

func (h hashRing) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h hashRing) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

type ConsistentBalancer struct {
	mu          sync.Mutex
	subConns    map[uint32]balancer.SubConn
	hashes      hashRing
	hashMapping map[balancer.SubConn]uint32
}

func (b *ConsistentBalancer) hash(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

func (b *ConsistentBalancer) AddSubConn(sc balancer.SubConn) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := fmt.Sprintf("%v", sc)
	hash := b.hash(key)

	// 把SubConn按哈希值映射到哈希环上
	b.hashes = append(b.hashes, hash)
	sort.Sort(b.hashes)

	b.subConns[hash] = sc
	b.hashMapping[sc] = hash
}

func (b *ConsistentBalancer) RemoveSubConn(sc balancer.SubConn) {
	b.mu.Lock()
	defer b.mu.Unlock()

	hash, ok := b.hashMapping[sc]
	if !ok {
		return
	}

	for i, h := range b.hashes {
		if h == hash {
			b.hashes = append(b.hashes[:i], b.hashes[i+1:]...)
			break
		}
	}
	delete(b.subConns, hash)
	delete(b.hashMapping, sc)
}

func (b *ConsistentBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.hashes) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	hash := b.hash(info.FullMethodName)

	idx := sort.Search(len(b.hashes), func(i int) bool {
		return b.hashes[i] >= hash
	})
	if idx == len(b.hashes) {
		idx = 0
	}

	chosenHash := b.hashes[idx]
	sc := b.subConns[chosenHash]

	return balancer.PickResult{
		SubConn: sc,
		Done:    func(info balancer.DoneInfo) {},
	}, nil
}

type ConsistentBalancerBuilder struct{}

func (c *ConsistentBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	cb := &ConsistentBalancer{
		subConns:    make(map[uint32]balancer.SubConn),
		hashes:      make(hashRing, 0),
		hashMapping: make(map[balancer.SubConn]uint32),
	}
	for subConn := range info.ReadySCs {
		cb.AddSubConn(subConn)
	}
	return cb
}
