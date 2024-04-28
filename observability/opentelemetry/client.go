package opentelemetry

import (
	"context"
	"github.com/dormoron/eidola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"log"
	"time"
)

// ClientInterceptorBuilder is used to create a UnaryClientInterceptor that collects metrics on gRPC client requests.
type ClientInterceptorBuilder struct {
	Namespace string // The Prometheus namespace for the metrics.
	Subsystem string // The Prometheus subsystem for the metrics.
	Name      string // The base name for the metrics to be built.
	Help      string // Help provides the description of the metrics.
}

// BuildUnary builds and returns a new grpc.UnaryClientInterceptor
// that will record response time, error count, and active requests metrics.
func (b ClientInterceptorBuilder) BuildUnary() grpc.UnaryClientInterceptor {
	address := observability.GetOutboundIP()

	// Define metric names with a clear and unique suffix.
	responseMetricName := b.Name + "_response_time_ms"
	errorCountMetricName := b.Name + "_errors_total"
	activeRequestCountMetricName := b.Name + "_active_requests"

	// Create a SummaryVec to track the response time of requests.
	summaryVec := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      responseMetricName,
		Help:      b.Help,
		Objectives: map[float64]float64{
			0.5:  0.05,
			0.9:  0.01,
			0.95: 0.005,
			0.99: 0.001,
		},
	}, []string{"method", "address", "kind"})

	// Create a CounterVec to track the count of errors.
	errCntVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      errorCountMetricName,
		Help:      "The total count of errors",
	}, []string{"method", "address", "kind"})

	// Create a GaugeVec to monitor the active request counts.
	reqCntVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: b.Namespace,
		Subsystem: b.Subsystem,
		Name:      activeRequestCountMetricName,
		Help:      "The current number of active requests",
	}, []string{"method", "address", "kind"})

	// Register the metrics. Ideally handle error in production code rather than panicking.
	if err := prometheus.Register(summaryVec); err != nil {
		log.Printf("Error registering summaryVec: %v", err)
	}
	if err := prometheus.Register(errCntVec); err != nil {
		log.Printf("Error registering errCntVec: %v", err)
	}
	if err := prometheus.Register(reqCntVec); err != nil {
		log.Printf("Error registering reqCntVec: %v", err)
	}

	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) (err error) {
		methodLabelValues := prometheus.Labels{"method": method, "address": address, "kind": "client"}

		// Increment active request count.
		reqCntVec.With(methodLabelValues).Inc()
		startTime := time.Now()

		defer func() {
			duration := time.Since(startTime)
			reqCntVec.With(methodLabelValues).Dec() // Decrement active request count.

			if err != nil {
				errCntVec.With(methodLabelValues).Inc() // Increment error count if there was an error.
			}

			summaryVec.With(methodLabelValues).Observe(duration.Seconds() * 1000) // Record response time in milliseconds.
		}()

		// Invoke the original gRPC call.
		err = invoker(ctx, method, req, reply, cc, opts...)
		return err
	}
}
