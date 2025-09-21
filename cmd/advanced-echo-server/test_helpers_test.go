package main

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"net/http/httptest"

	"github.com/prometheus/client_golang/prometheus"
)

// flusherResponseWriter supports http.Flusher for SSE tests using httptest
type flusherResponseWriter struct {
	*httptest.ResponseRecorder
}

func (frw *flusherResponseWriter) Flush() {
	// No-op for testing; httptest doesn't write to a real connection
}

var (
	testRegistry *prometheus.Registry
)

// setupTest resets global state and reinitializes metrics for isolation
func setupTest() {
	configLock.Lock()
	defer configLock.Unlock()

	// Reset global state
	scenarios = sync.Map{}
	scenarioIndex = sync.Map{}
	historyMutex.Lock()
	requestHistory = make([]RequestRecord, 0, config.HistorySize)
	historyMutex.Unlock()
	atomic.StoreUint64(&requestCounter, 0) // thread-safe reset
	rateLimiter = nil
	config = Config{
		Port:           "8080",
		EnableCORS:     true,
		LogRequests:    true,
		LogHeaders:     false,
		LogBody:        false,
		MaxBodySize:    10485760,
		HistorySize:    100,
		RateLimitRPS:   0,
		RateLimitBurst: 0,
	}

	// Reset Prometheus metrics in a fresh registry
	testRegistry = prometheus.NewRegistry()
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
			Buckets: prometheus.LinearBuckets(0.01, 0.05, 10),
		},
	)
	chaosErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "echo_chaos_errors_total",
			Help: "Total number of chaos-induced errors",
		},
		[]string{"type"},
	)
	testRegistry.MustRegister(requestTotal, requestLatency, chaosErrors)
}

func TestMain(m *testing.M) {
	// Ensure clean state for tests
	setupTest()
	os.Exit(m.Run())
}
