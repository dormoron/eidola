package metrics

import (
	"context"
	"github.com/dormoron/eidola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"time"
)

type ClientInterceptorBuilder struct {
	Namespace string
	Subsystem string
	Name      string
	Help      string
}

func (b ClientInterceptorBuilder) BuildUnary() grpc.UnaryClientInterceptor {
	address := observability.GetOutboundIP()
	summaryVec := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_response",
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "client",
		},
		Objectives: map[float64]float64{
			0.5:   0.01,
			0.75:  0.01,
			0.9:   0.01,
			0.99:  0.001,
			0.999: 0.0001,
		},
	}, []string{"method"})

	errCntVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_error_cnt",
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "client",
		},
	}, []string{"method"})

	reqCntVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_active_req_cnt",
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "client",
		},
	}, []string{"method"})
	prometheus.MustRegister(summaryVec, errCntVec, reqCntVec)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) (err error) {
		reqCnt := reqCntVec.WithLabelValues(method)
		reqCnt.Add(1)
		startTime := time.Now()
		defer func() {
			if err != nil {
				errCntVec.WithLabelValues(method).Add(1)
			}
			duration := time.Now().Sub(startTime)
			reqCnt.Sub(1)
			summaryVec.WithLabelValues(method).Observe(float64(duration.Milliseconds()))
		}()
		err = invoker(ctx, method, req, reply, cc, opts...)
		return
	}
}
