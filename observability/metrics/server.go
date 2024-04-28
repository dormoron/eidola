package metrics

import (
	"context"
	"fmt"
	"github.com/dormoron/eidola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"log"
	"time"
)

// ServerMetricsBuilder is used to create Prometheus metrics for gRPC servers such as request counts, errors, and request durations.
type ServerMetricsBuilder struct {
	Namespace string
	Subsystem string
	Name      string
	Help      string
	Port      string
}

// Build creates and registers Prometheus metrics, returning a new gRPC UnaryServerInterceptor.
func (b ServerMetricsBuilder) Build() grpc.UnaryServerInterceptor {
	address := observability.GetOutboundIP()
	if b.Port != "" {
		address = fmt.Sprintf("%s:%s", address, b.Port) // Use string formatting for better readability
	}

	// Create a Gauge metric to track the number of in-flight requests
	reqGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_in_flight_requests",
		Help:      b.Help,
	}, []string{"method"})

	// Create a Counter metric to count the number of errors
	errCnt := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_errors_total",
		Help:      b.Help,
	}, []string{"method"})

	// Create a Summary to measure the latency of requests
	summary := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      b.Name + "_response_latency_milliseconds",
		Help:      b.Help,
		Objectives: map[float64]float64{
			0.5:   0.01,
			0.75:  0.01,
			0.9:   0.01,
			0.99:  0.001,
			0.999: 0.0001,
		},
	}, []string{"method"})

	// Register the metrics with Prometheus
	if err := prometheus.Register(reqGauge); err != nil {
		log.Printf("Error registering reqGauge: %v", err)
	}
	if err := prometheus.Register(errCnt); err != nil {
		log.Printf("Error registering errCnt: %v", err)
	}
	if err := prometheus.Register(summary); err != nil {
		log.Printf("Error registering summary: %v", err)
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		methodLabel := info.FullMethod

		// Increment in-flight request gauge
		reqGauge.WithLabelValues(methodLabel).Inc()
		startTime := time.Now()
		defer func() {
			// Decrement in-flight request gauge and record error and latency
			reqGauge.WithLabelValues(methodLabel).Dec()
			if err != nil {
				errCnt.WithLabelValues(methodLabel).Inc()
			}
			summary.WithLabelValues(methodLabel).Observe(float64(time.Since(startTime).Milliseconds()))
		}()

		// Call the handler
		resp, err = handler(ctx, req)
		return resp, err
	}
}
