package opentelemetry

import (
	"context"
	"fmt"
	"github.com/dormoron/eidola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"log"
	"time"
)

// ServerInterceptorBuilder is a struct used to configure and build UnaryServerInterceptor
// for gRPC, focusing on metrics like response time, error count, and active request count.
type ServerInterceptorBuilder struct {
	Namespace string // Namespace for Prometheus metrics
	Subsystem string // Subsystem is the subset of the namespace
	Name      string // Name of the metric
	Help      string // Help provides some description about the metric

	Port string // Port where the server is running. If not empty, it will be appended to the address label.
}

// BuildUnary constructs a UnaryServerInterceptor with Prometheus monitoring.
func (b ServerInterceptorBuilder) BuildUnary() grpc.UnaryServerInterceptor {
	address := observability.GetOutboundIP()
	if b.Port != "" {
		address = fmt.Sprintf("%s:%s", address, b.Port) // Use fmt.Sprintf for string concatenation
	}

	// Define Prometheus SummaryVec for response latency
	summaryVec := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_response", b.Name),
		Help:      b.Help,
		// Labels added to all metrics
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
		// Customize objectives
		Objectives: map[float64]float64{
			0.5:   0.01,
			0.75:  0.01,
			0.9:   0.01,
			0.99:  0.001,
			0.999: 0.0001,
		},
	}, []string{"method"})

	// Define Prometheus CounterVec for error count
	errCntVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_error_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method"})

	// Define Prometheus GaugeVec for active request count
	reqCntVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      fmt.Sprintf("%s_active_req_cnt", b.Name),
		Help:      b.Help,
		ConstLabels: map[string]string{
			"address": address,
			"kind":    "server",
		},
	}, []string{"method"})

	// Register defined metrics with Prometheus
	if err := prometheus.Register(summaryVec); err != nil {
		log.Printf("Error registering summaryVec: %v", err)
	}
	if err := prometheus.Register(errCntVec); err != nil {
		log.Printf("Error registering errCntVec: %v", err)
	}
	if err := prometheus.Register(reqCntVec); err != nil {
		log.Printf("Error registering reqCntVec: %v", err)
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		reqCnt := reqCntVec.WithLabelValues(info.FullMethod)
		reqCnt.Inc()
		startTime := time.Now()
		defer func() {
			if err != nil {
				errCntVec.WithLabelValues(info.FullMethod).Inc()
			}
			duration := time.Since(startTime)
			reqCnt.Dec()
			summaryVec.WithLabelValues(info.FullMethod).Observe(float64(duration.Milliseconds()))
		}()
		return handler(ctx, req)
	}
}
