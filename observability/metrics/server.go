package metrics

import (
	"context"
	"eidola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"time"
)

type ServerMetricsBuilder struct {
	Namespace string
	Subsystem string
	Name      string
	Help      string
	Port      string
}

func (b *ServerMetricsBuilder) Build() grpc.UnaryServerInterceptor {
	address := observability.GetOutboundIP()
	if b.Port != "" {
		address = address + ":" + b.Port
	}
	reqGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_response",
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"server"})
	errCnt := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_response",
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"server"})
	summary := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_response",
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
		Objectives: map[float64]float64{
			0.5:   0.01,
			0.75:  0.01,
			0.9:   0.01,
			0.99:  0.001,
			0.999: 0.0001,
		},
	}, []string{"server"})
	prometheus.MustRegister(reqGauge, errCnt, summary)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		startTime := time.Now()
		reqGauge.WithLabelValues(info.FullMethod).Add(1)
		defer func() {
			reqGauge.WithLabelValues(info.FullMethod).Add(-1)
			if err != nil {
				errCnt.WithLabelValues(info.FullMethod).Add(1)
			}
			summary.WithLabelValues(info.FullMethod).
				Observe(float64(time.Now().Sub(startTime).Milliseconds()))
		}()
		resp, err = handler(ctx, req)
		return
	}

}
