package fastest

import (
	"encoding/json"
	"fmt"
	"github.com/dormoron/eidola/loadbalance"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Conn represents a connection with its address and response time.
type Conn struct {
	SubConn  balancer.SubConn
	Address  resolver.Address
	Response time.Duration // Response time of the connection.
}

// Balancer is a custom load balancer that selects connections based on their response time.
type Balancer struct {
	mutex    sync.RWMutex
	conns    []*Conn
	Filter   loadbalance.Filter
	LastSync time.Time
	Endpoint string
}

// Pick selects the connection with the least response time.
func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	if len(b.conns) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	var res *Conn
	for _, c := range b.conns {
		if b.Filter != nil && !b.Filter(info, c.Address) {
			continue
		}
		if res == nil || res.Response > c.Response {
			res = c
		}
	}
	if res == nil {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	return balancer.PickResult{
		SubConn: res.SubConn,
		Done:    func(info balancer.DoneInfo) {},
	}, nil
}

type Builder struct {
	Filter   loadbalance.Filter
	Endpoint string
	Query    string
	Interval time.Duration
}

// Build creates a new Balancer and starts a background goroutine to update connections' response times.
func (b *Builder) Build(info base.PickerBuildInfo) balancer.Picker {
	conns := make([]*Conn, 0, len(info.ReadySCs))
	for con, val := range info.ReadySCs {
		conns = append(conns, &Conn{
			SubConn:  con,
			Address:  val.Address,
			Response: time.Millisecond * 100, // Default response time.
		})
	}
	balancer := &Balancer{
		conns:  conns,
		Filter: b.Filter,
	}

	go balancer.startUpdateRoutine(b.Endpoint, b.Query, b.Interval)

	return balancer
}

// startUpdateRoutine periodically updates the response times of connections.
func (b *Balancer) startUpdateRoutine(endpoint, query string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.updateRespTime(endpoint, query)
		}
	}
}

// updateRespTime updates response times based on metrics obtained from an external source.
func (b *Balancer) updateRespTime(endpoint, query string) {
	httpResp, err := http.Get(fmt.Sprintf("%s/api/v1/query?query=%s", endpoint, query))
	if err != nil {
		log.Println("Failed to query Prometheus:", err)
		return
	}
	defer httpResp.Body.Close()

	var resp response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		log.Println("Failed to deserialize http response:", err)
		return
	}

	if resp.Status != "success" {
		log.Println("Failed response")
		return
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, promRes := range resp.Data.Result {
		address, ok := promRes.Metric["address"]
		if !ok {
			continue
		}

		for _, c := range b.conns {
			if c.Address.Addr == address {
				ms, err := strconv.ParseInt(promRes.Value[1].(string), 10, 64)
				if err != nil {
					log.Println("Failed to parse response time:", err)
					continue
				}
				c.Response = time.Duration(ms) * time.Millisecond
			}
		}
	}
}

type response struct {
	Status string `json:"status"`
	Data   data   `json:"data"`
}

type data struct {
	ResultType string   `json:"resultType"`
	Result     []result `json:"result"`
}

type result struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}
