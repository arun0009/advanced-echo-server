package main

import "github.com/prometheus/client_golang/prometheus"

// Prometheus metrics
var (
	requestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "echo_requests_total",
			Help: "Total number of requests processed",
		},
		[]string{"method", "path", "status"},
	)
	requestLatency = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "echo_request_duration_seconds",
			Help:    "Request latency in seconds",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 15), // ~10ms to ~163s
		},
	)
	chaosErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "echo_chaos_errors_total",
			Help: "Total number of chaos-induced errors",
		},
		[]string{"type"},
	)
)

// registerPrometheusMetrics registers the collectors with the default registry.
func registerPrometheusMetrics() {
	prometheus.MustRegister(requestTotal, requestLatency, chaosErrors)
}
