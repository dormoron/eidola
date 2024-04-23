package fastest

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"sync"
	"time"
)

import (
	"google.golang.org/grpc/connectivity"
)

type SubConnInfo struct {
	subConn      balancer.SubConn
	avgResponse  time.Duration
	mu           sync.RWMutex // Protect avgResponse
	requestCount int64        // To calculate average
}

type ResponseBalancer struct {
	mu           sync.Mutex
	subConns     map[balancer.SubConn]*SubConnInfo
	minRTSubConn balancer.SubConn
}

func NewResponseBalancer() *ResponseBalancer {
	return &ResponseBalancer{
		subConns: make(map[balancer.SubConn]*SubConnInfo),
	}
}

func (rb *ResponseBalancer) AddSubConn(sc balancer.SubConn) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.subConns[sc] = &SubConnInfo{
		subConn:      sc,
		avgResponse:  0,
		requestCount: 0,
	}
	if rb.minRTSubConn == nil {
		rb.minRTSubConn = sc // Initialize to the first SubConn
	}
}

func (rb *ResponseBalancer) RemoveSubConn(sc balancer.SubConn) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	delete(rb.subConns, sc)
	if rb.minRTSubConn == sc {
		rb.minRTSubConn = nil        // Reset the minRTSubConn
		for k := range rb.subConns { // Find a new candidate for minRT
			rb.minRTSubConn = k
			break
		}
	}
}

func (rb *ResponseBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	if state.ConnectivityState == connectivity.TransientFailure || state.ConnectivityState == connectivity.Shutdown {
		rb.RemoveSubConn(sc)
	}
	// Other state updates could be handled here based on requirements.
}

func (rb *ResponseBalancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if len(rb.subConns) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	minRTSubConn := rb.minRTSubConn
	startTime := time.Now()
	return balancer.PickResult{
		SubConn: minRTSubConn,
		Done: func(doneInfo balancer.DoneInfo) {
			rb.handleDoneInfo(minRTSubConn, startTime, doneInfo)
		},
	}, nil
}

func (rb *ResponseBalancer) handleDoneInfo(sc balancer.SubConn, startTime time.Time, doneInfo balancer.DoneInfo) {
	if doneInfo.Err != nil {
		return
	}

	rt := time.Since(startTime) // Calculate request response time

	rb.mu.Lock()
	scInfo, ok := rb.subConns[sc]
	rb.mu.Unlock()
	if !ok {
		return
	}

	scInfo.mu.Lock()
	defer scInfo.mu.Unlock()
	// Update the average response time and request counter
	scInfo.requestCount++
	totalDuration := scInfo.avgResponse*time.Duration(scInfo.requestCount-1) + rt
	scInfo.avgResponse = totalDuration / time.Duration(scInfo.requestCount)

	rb.mu.Lock()
	defer rb.mu.Unlock()
	// If the current SubConn has a better average response time, update minRTSubConn
	if scInfo.avgResponse < rb.subConns[rb.minRTSubConn].avgResponse {
		rb.minRTSubConn = sc
	}
}

type ResponseBalancerBuilder struct {
}

func (bb *ResponseBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	rb := NewResponseBalancer()
	for sc := range info.ReadySCs {
		rb.AddSubConn(sc)
	}
	return rb
}

func (*ResponseBalancerBuilder) Name() string {
	return "fastest_response"
}

// main function or init function in your package
func init() {
	balancer.Register(base.NewBalancerBuilder("fastest_response", &ResponseBalancerBuilder{}, base.Config{}))
}
