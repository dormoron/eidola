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
	"runtime"
	"strconv"
	"sync"
	"time"
)

type Balancer struct {
	mutex    sync.RWMutex
	conns    []*conn
	filter   loadbalance.Filter
	lastSync time.Time
	endpoint string
}

func (b *Balancer) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	b.mutex.RLock()
	if len(b.conns) == 0 {
		b.mutex.RUnlock()
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	var res *conn
	for _, c := range b.conns {
		if !b.filter(info, c.address) {
			continue
		}
		if res == nil {
			res = c
		} else if res.response > c.response {
			res = c
		}
	}
	b.mutex.RUnlock()

	return balancer.PickResult{
		SubConn: res.SubConn,
		Done: func(info balancer.DoneInfo) {
		},
	}, nil
}

func (b *Builder) Build(info base.PickerBuildInfo) balancer.Picker {
	conns := make([]*conn, 0, len(info.ReadySCs))
	for con, val := range info.ReadySCs {
		conns = append(conns, &conn{
			SubConn:  con,
			address:  val.Address,
			response: time.Millisecond * 100,
		})
	}
	flt := b.Filter
	if flt == nil {
		flt = func(info balancer.PickInfo, address resolver.Address) bool {
			return true
		}
	}
	res := &Balancer{
		conns:  conns,
		filter: flt,
	}

	ch := make(chan struct{}, 1)
	runtime.SetFinalizer(res, func() {
		ch <- struct{}{}
	})
	go func() {
		ticker := time.NewTicker(b.Interval)
		for {
			select {
			case <-ticker.C:
				res.updateRespTime(b.Endpoint, b.Query)
			case <-ch:
				return
			}
		}
	}()
	return res
}

func (b *Balancer) updateRespTime(endpoint, query string) {
	httpResp, err := http.Get(fmt.Sprintf("%s/api/v1/query?query=%s", endpoint, query))
	if err != nil {
		log.Fatalln("Failed to query prometheus", err)
		return
	}
	decoder := json.NewDecoder(httpResp.Body)

	var resp response
	err = decoder.Decode(&resp)
	if err != nil {
		log.Fatalln("Failed to deserialize http response", err)
		return
	}
	if resp.Status != "success" {
		log.Fatalln("Failed response", err)
		return
	}
	for _, promRes := range resp.Data.Result {
		address, ok := promRes.Metric["address"]
		if !ok {
			return
		}

		for _, c := range b.conns {
			if c.address.Addr == address {
				ms, err := strconv.ParseInt(promRes.Value[1].(string), 10, 64)
				if err != nil {
					continue
				}
				c.response = time.Duration(ms) * time.Millisecond
			}
		}
	}
}

type Builder struct {
	Filter   loadbalance.Filter
	Endpoint string
	Query    string
	Interval time.Duration
}

type conn struct {
	balancer.SubConn
	address resolver.Address
	// 响应时间
	response time.Duration
}

type response struct {
	Status string `json:"status"`
	Data   data   `json:"data"`
}

type data struct {
	ResultType string   `json:"resultType"`
	Result     []Result `json:"result"`
}

type Result struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}
