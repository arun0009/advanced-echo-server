package main

import (
	"bytes"
	"compress/gzip"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

// Config holds server configuration
type Config struct {
	Port           string
	EnableTLS      bool
	CertFile       string
	KeyFile        string
	EnableCORS     bool
	LogRequests    bool
	LogHeaders     bool
	LogBody        bool
	MaxBodySize    int64
	Hostname       string
	HistorySize    int
	ScenarioFile   string
	RateLimitRPS   float64
	RateLimitBurst int
}

// RequestRecord stores request details for history/replay
type RequestRecord struct {
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Method    string      `json:"method"`
	URL       string      `json:"url"`
	Headers   http.Header `json:"headers"`
	Body      []byte      `json:"body"`
}

// Scenario defines a sequence of responses for an endpoint
type Scenario struct {
	Path      string     `yaml:"path" json:"path"`
	Responses []Response `yaml:"responses" json:"responses"`
}

// Response defines a single response in a scenario
type Response struct {
	Status int    `yaml:"status" json:"status"`
	Delay  string `yaml:"delay" json:"delay"` // e.g., "500ms", "100-500ms"
	Body   string `yaml:"body" json:"body"`
}

//go:embed html/*
var files embed.FS

// Global state for metrics, counters, and scenarios
var (
	config    Config
	startTime time.Time
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	rng            = rand.New(rand.NewSource(time.Now().UnixNano()))
	requestCounter uint64
	counterMutex   sync.Mutex
	scenarios      sync.Map // map[string][]Response (path -> response sequence)
	scenarioIndex  sync.Map // map[string]int (path -> current index)
	requestHistory []RequestRecord
	historyMutex   sync.Mutex
	rateLimiter    *rate.Limiter
)

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
)

func init() {
	startTime = time.Now()

	// Load configuration from environment variables
	config = Config{
		Port:           getEnv("PORT", "8080"),
		EnableTLS:      getEnv("ENABLE_TLS", "false") == "true",
		CertFile:       getEnv("CERT_FILE", "server.crt"),
		KeyFile:        getEnv("KEY_FILE", "server.key"),
		EnableCORS:     getEnv("ENABLE_CORS", "true") == "true",
		LogRequests:    getEnv("LOG_REQUESTS", "true") == "true",
		LogHeaders:     getEnv("LOG_HEADERS", "false") == "true",
		LogBody:        getEnv("LOG_BODY", "false") == "true",
		MaxBodySize:    parseInt64(getEnv("MAX_BODY_SIZE", "10485760")), // 10MB
		HistorySize:    int(parseInt64(getEnv("ECHO_HISTORY_SIZE", "100"))),
		ScenarioFile:   getEnv("ECHO_SCENARIO_FILE", "scenarios.yaml"),
		RateLimitRPS:   parseFloat64(getEnv("ECHO_RATE_LIMIT_RPS", "0")),
		RateLimitBurst: int(parseInt64(getEnv("ECHO_RATE_LIMIT_BURST", "0"))),
	}

	hostname, _ := os.Hostname()
	config.Hostname = hostname

	// Initialize request history
	if config.HistorySize > 0 {
		requestHistory = make([]RequestRecord, 0, config.HistorySize)
	}

	// Initialize rate limiter
	if config.RateLimitRPS > 0 && config.RateLimitBurst > 0 {
		rateLimiter = rate.NewLimiter(rate.Limit(config.RateLimitRPS), config.RateLimitBurst)
	}

	// Load scenarios from YAML if specified
	if config.ScenarioFile != "" {
		if data, err := os.ReadFile(config.ScenarioFile); err == nil {
			var sc []Scenario
			if err := yaml.Unmarshal(data, &sc); err == nil {
				for _, s := range sc {
					scenarios.Store(s.Path, s.Responses)
					scenarioIndex.Store(s.Path, 0)
				}
			} else {
				log.Printf("Failed to parse scenario file: %v", err)
			}
		}
	}

	// Register Prometheus metrics
	prometheus.MustRegister(requestTotal, requestLatency, chaosErrors)
}

func setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Apply middleware
	router.Use(loggingMiddleware)
	router.Use(corsMiddleware)
	router.Use(requestIDMiddleware)
	if rateLimiter != nil {
		router.Use(rateLimitMiddleware)
	}

	// Health check endpoints
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/ready", readyHandler).Methods("GET")

	// Server info
	router.HandleFunc("/info", infoHandler).Methods("GET")

	// WebSocket and SSE endpoints
	router.HandleFunc("/ws", websocketHandler)
	router.HandleFunc("/sse", sseHandler)

	// Embedded frontend for WebSocket and SSE
	router.HandleFunc("/web-ws", serveFrontendWS)
	router.HandleFunc("/web-sse", serveFrontendSSE)

	// Request history and replay
	router.HandleFunc("/history", historyHandler).Methods("GET")
	router.HandleFunc("/replay", replayHandler).Methods("POST")

	// Scenario management
	router.HandleFunc("/scenario", scenarioHandler).Methods("GET", "POST")

	// Prometheus metrics
	router.Handle("/metrics", promhttp.Handler())

	// Everything else is pure echo with testing features
	router.PathPrefix("/").HandlerFunc(echoHandler)

	return router
}

// Middleware functions
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if config.LogRequests {
			log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		}

		if config.LogHeaders {
			log.Printf("Headers: %+v", r.Header)
		}

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		if config.LogRequests {
			log.Printf("%s %s %s - %d %v", r.RemoteAddr, r.Method, r.URL.Path, rw.statusCode, time.Since(start))
		}

		// Record latency for Prometheus
		requestLatency.Observe(time.Since(start).Seconds())
		requestTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(rw.statusCode)).Inc()
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.EnableCORS {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Expose-Headers", "*")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
			r.Header.Set("X-Request-ID", requestID)
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r)
	})
}

func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rateLimiter.Allow() {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			w.Header().Set("Retry-After", "60")
			chaosErrors.WithLabelValues("rate_limit").Inc()
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Custom response writer to capture status code and support Flusher
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Main Echo Handler
func echoHandler(w http.ResponseWriter, r *http.Request) {
	// Increment request counter
	counterMutex.Lock()
	requestCounter++
	currentCount := requestCounter
	counterMutex.Unlock()

	// Record request for history
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, config.MaxBodySize))
		if err != nil {
			http.Error(w, "Error reading body: "+err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body)) // Restore body for processing
	} else {
		body = []byte{} // Empty body for nil case
	}
	if config.HistorySize > 0 {
		recordRequest(r, body)
	}

	// Apply delays from headers or environment variables
	applyDelays(r)

	// Process testing features (headers and environment variables)
	if processTestingFeatures(w, r, body) {
		return
	}

	// Fall back to scenario responses
	if processScenario(w, r) {
		return
	}

	// Echo back custom headers
	echoCustomHeaders(w, r)
	w.Header().Set("X-Echo-Request-Count", strconv.FormatUint(currentCount, 10))

	// Set dynamic response headers from environment variables
	setEnvHeaders(w)

	// Handle body based on request type
	var responseBody []byte
	if r.Method == "GET" && len(body) == 0 {
		responseBody = []byte(echoRequestInfo(r))
		w.Header().Set("Content-Type", "text/plain")
	} else {
		// Dynamic response generation
		if sizeHeader := r.Header.Get("X-Echo-Response-Size"); sizeHeader != "" {
			size, err := strconv.Atoi(sizeHeader)
			if err == nil && size > 0 {
				responseBody = make([]byte, size)
				rand.Read(responseBody)
				w.Header().Set("Content-Type", "application/octet-stream")
			}
		} else {
			responseBody = body
			setResponseContentType(w, r)
		}
	}

	// Handle GZIP compression
	if compressHeader := r.Header.Get("X-Echo-Compress"); strings.ToLower(compressHeader) == "gzip" {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		gz.Write(responseBody)
		gz.Close()
		responseBody = buf.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// Process scenario responses
func processScenario(w http.ResponseWriter, r *http.Request) bool {
	scenario, ok := scenarios.Load(r.URL.Path)
	if !ok {
		return false
	}
	responses := scenario.([]Response)
	idx, _ := scenarioIndex.LoadOrStore(r.URL.Path, 0)
	index := idx.(int) % len(responses)
	resp := responses[index]
	scenarioIndex.Store(r.URL.Path, index+1) // Increment index for next request

	// Apply delay from scenario
	if resp.Delay != "" {
		if strings.Contains(resp.Delay, "-") {
			// Random delay
			parts := strings.Split(resp.Delay, "-")
			if len(parts) == 2 {
				minStr := strings.TrimSuffix(parts[0], "ms")
				maxStr := strings.TrimSuffix(parts[1], "ms")
				min, err1 := strconv.Atoi(minStr)
				max, err2 := strconv.Atoi(maxStr)
				if err1 == nil && err2 == nil && max >= min {
					delay := min + rng.Intn(max-min+1)
					log.Printf("Scenario delay: %dms (range: %d-%d)", delay, min, max)
					time.Sleep(time.Duration(delay) * time.Millisecond)
				}
			}
		} else {
			// Fixed delay
			delayStr := strings.TrimSuffix(resp.Delay, "ms")
			if ms, err := strconv.Atoi(delayStr); err == nil {
				if ms > 300000 {
					ms = 300000
				}
				log.Printf("Scenario delay: %dms", ms)
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
		}
	}

	// Set response headers and body
	w.Header().Set("X-Echo-Scenario", "true")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	if resp.Body != "" {
		w.Write([]byte(resp.Body))
	} else {
		w.Write([]byte(echoRequestInfo(r)))
	}
	return true
}

// Process testing features
func processTestingFeatures(w http.ResponseWriter, r *http.Request, body []byte) bool {
	// Force HTTP status code
	statusStr := getHeaderOrEnv(r, "X-Echo-Status", "ECHO_STATUS")
	if statusStr != "" {
		if status, err := strconv.Atoi(statusStr); err == nil && status >= 100 && status <= 599 {
			w.Header().Set("X-Echo-Status-Forced", "true")
			var responseBody []byte
			if r.Method == "GET" && len(body) == 0 {
				responseBody = []byte(echoRequestInfo(r))
				w.Header().Set("Content-Type", "text/plain")
			} else {
				responseBody = body
				setResponseContentType(w, r)
			}
			w.WriteHeader(status)
			w.Write(responseBody)
			chaosErrors.WithLabelValues("status").Inc()
			return true
		}
	}

	// Force specific errors
	errorStr := getHeaderOrEnv(r, "X-Echo-Error", "ECHO_ERROR")
	if errorStr != "" {
		switch strings.ToLower(errorStr) {
		case "timeout":
			time.Sleep(65 * time.Second)
			chaosErrors.WithLabelValues("timeout").Inc()
			return true
		case "500", "internal":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Simulated internal server error"))
			chaosErrors.WithLabelValues("internal").Inc()
			return true
		case "502", "bad-gateway":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("Simulated bad gateway"))
			chaosErrors.WithLabelValues("bad_gateway").Inc()
			return true
		case "503", "unavailable":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Simulated service unavailable"))
			chaosErrors.WithLabelValues("unavailable").Inc()
			return true
		case "504", "gateway-timeout":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte("Simulated gateway timeout"))
			chaosErrors.WithLabelValues("gateway_timeout").Inc()
			return true
		case "429", "rate-limit":
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Simulated rate limit exceeded"))
			chaosErrors.WithLabelValues("rate_limit").Inc()
			return true
		case "random":
			errors := []int{500, 502, 503, 504, 429}
			status := errors[rng.Intn(len(errors))]
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			w.Write([]byte(fmt.Sprintf("Random simulated error: %d", status)))
			chaosErrors.WithLabelValues("random").Inc()
			return true
		}
	}

	// Chaos engineering - random failures
	chaosStr := getHeaderOrEnv(r, "X-Echo-Chaos", "ECHO_CHAOS")
	if chaosStr != "" {
		if rate, err := strconv.Atoi(chaosStr); err == nil && rate > 0 && rate <= 100 {
			if rng.Intn(100) < rate {
				errors := []int{500, 502, 503, 504, 408, 429}
				status := errors[rng.Intn(len(errors))]
				log.Printf("Chaos: Injecting %d error for %s (%d%% rate)", status, r.RemoteAddr, rate)
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				w.Write([]byte(fmt.Sprintf("Chaos error injection: %d", status)))
				chaosErrors.WithLabelValues("chaos").Inc()
				return true
			}
		}
	}

	return false
}

// Apply various delay patterns
func applyDelays(r *http.Request) {
	// Simple delay
	delayStr := getHeaderOrEnv(r, "X-Echo-Delay", "ECHO_DELAY")
	if delayStr != "" {
		// Handle "ms" suffix
		delayStr = strings.TrimSuffix(delayStr, "ms")
		if delayMs, err := strconv.Atoi(delayStr); err == nil && delayMs > 0 {
			maxDelay := 300000 // 5 minutes max
			if delayMs > maxDelay {
				delayMs = maxDelay
			}
			log.Printf("Simple delay: %dms", delayMs)
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
			return
		}
	}

	// Jitter delay
	jitterStr := getHeaderOrEnv(r, "X-Echo-Jitter", "ECHO_JITTER")
	if jitterStr != "" {
		parts := strings.Split(jitterStr, ",")
		if len(parts) == 2 {
			if base, err1 := strconv.Atoi(parts[0]); err1 == nil {
				if variance, err2 := strconv.Atoi(parts[1]); err2 == nil && variance >= 0 {
					jitter := rng.Intn(variance*2) - variance
					totalDelay := base + jitter
					if totalDelay < 0 {
						totalDelay = 0
					}
					if totalDelay > 300000 {
						totalDelay = 300000
					}
					log.Printf("Jitter delay: %dms (base: %dms, jitter: %dms)", totalDelay, base, jitter)
					time.Sleep(time.Duration(totalDelay) * time.Millisecond)
					return
				}
			}
		}
	}

	// Random delay
	randomStr := getHeaderOrEnv(r, "X-Echo-Random-Delay", "ECHO_RANDOM_DELAY")
	if randomStr != "" {
		parts := strings.Split(randomStr, ",")
		if len(parts) == 2 {
			if min, err1 := strconv.Atoi(parts[0]); err1 == nil {
				if max, err2 := strconv.Atoi(parts[1]); err2 == nil && max >= min {
					if max > 300000 {
						max = 300000
					}
					delay := min + rng.Intn(max-min+1)
					log.Printf("Random delay: %dms (range: %d-%d)", delay, min, max)
					time.Sleep(time.Duration(delay) * time.Millisecond)
					return
				}
			}
		}
	}

	// Exponential backoff
	expStr := getHeaderOrEnv(r, "X-Echo-Exponential", "ECHO_EXPONENTIAL")
	if expStr != "" {
		parts := strings.Split(expStr, ",")
		if len(parts) == 2 {
			if base, err1 := strconv.Atoi(parts[0]); err1 == nil && base > 0 {
				if attempt, err2 := strconv.Atoi(parts[1]); err2 == nil && attempt > 0 {
					exponentialDelay := base * int(math.Pow(2, float64(attempt-1)))
					if exponentialDelay > 300000 {
						exponentialDelay = 300000
					}
					jitter := int(float64(exponentialDelay) * 0.25)
					finalDelay := exponentialDelay + rng.Intn(jitter*2) - jitter
					if finalDelay < 0 {
						finalDelay = 0
					}
					log.Printf("Exponential delay: %dms (base: %dms, attempt: %d)", finalDelay, base, attempt)
					time.Sleep(time.Duration(finalDelay) * time.Millisecond)
					return
				}
			}
		}
	}

	// Latency injection
	latencyStr := getHeaderOrEnv(r, "X-Echo-Latency", "ECHO_LATENCY")
	if latencyStr != "" {
		if strings.Contains(latencyStr, "-") {
			// Random latency
			parts := strings.Split(latencyStr, "-")
			if len(parts) == 2 {
				minStr := strings.TrimSuffix(parts[0], "ms")
				maxStr := strings.TrimSuffix(parts[1], "ms")
				min, _ := strconv.Atoi(minStr)
				max, _ := strconv.Atoi(maxStr)
				if max >= min {
					delay := min + rng.Intn(max-min+1)
					log.Printf("Latency injection: %dms (range: %d-%d)", delay, min, max)
					time.Sleep(time.Duration(delay) * time.Millisecond)
					return
				}
			}
		} else {
			// Fixed latency
			delayStr := strings.TrimSuffix(latencyStr, "ms")
			if ms, err := strconv.Atoi(delayStr); err == nil {
				if ms > 300000 {
					ms = 300000
				}
				log.Printf("Latency injection: %dms", ms)
				time.Sleep(time.Duration(ms) * time.Millisecond)
				return
			}
		}
	}
}

// Echo back custom headers
func echoCustomHeaders(w http.ResponseWriter, r *http.Request) {
	if echoHeaders := r.Header.Get("X-Echo-Headers"); echoHeaders != "" {
		headerNames := strings.Split(echoHeaders, ",")
		for _, headerName := range headerNames {
			headerName = strings.TrimSpace(headerName)
			if headerValue := r.Header.Get(headerName); headerValue != "" {
				w.Header().Set("X-Echoed-"+headerName, headerValue)
			}
		}
	}

	for name, values := range r.Header {
		if strings.HasPrefix(name, "X-Echo-Set-Header-") {
			headerName := strings.TrimPrefix(name, "X-Echo-Set-Header-")
			headerName = strings.ReplaceAll(headerName, "-", " ")
			headerName = strings.Title(headerName)
			headerName = strings.ReplaceAll(headerName, " ", "-")
			for _, value := range values {
				w.Header().Set(headerName, value)
			}
		}
	}

	if getHeaderOrEnv(r, "X-Echo-Server-Info", "ECHO_SERVER_INFO") == "true" {
		w.Header().Set("X-Echo-Server", config.Hostname)
		w.Header().Set("X-Echo-Version", "2.0.0")
		w.Header().Set("X-Echo-Uptime", time.Since(startTime).String())
	}
}

func setEnvHeaders(w http.ResponseWriter) {
	for _, line := range os.Environ() {
		parts := strings.SplitN(line, "=", 2)
		key, value := parts[0], parts[1]
		if name, ok := strings.CutPrefix(key, `ECHO_HEADER_`); ok {
			wrHeaderName := strings.ReplaceAll(name, "_", "-")
			w.Header().Set(wrHeaderName, value)
		}
	}
}

// Set appropriate content type
func setResponseContentType(w http.ResponseWriter, r *http.Request) {
	if contentType := r.Header.Get("X-Echo-Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
}

// Echo request information for GET requests without body
func echoRequestInfo(r *http.Request) string {
	var response strings.Builder
	uri := r.RequestURI
	if uri == "" {
		uri = "/"
	}
	response.WriteString(fmt.Sprintf("%s %s %s\n", r.Method, uri, r.Proto))
	response.WriteString(fmt.Sprintf("Host: %s\n", r.Host))
	var headerNames []string
	for name := range r.Header {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		for _, value := range r.Header[name] {
			response.WriteString(fmt.Sprintf("%s: %s\n", name, value))
		}
	}
	response.WriteString(fmt.Sprintf("\nClient-IP: %s\n", getClientIP(r)))
	response.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	return response.String()
}

// Health check handlers
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now(),
		"uptime":    time.Since(startTime).String(),
	})
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// Server info handler
func infoHandler(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, config.MaxBodySize))
	} else {
		body = []byte{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"timestamp":    time.Now(),
		"method":       r.Method,
		"url":          r.RequestURI,
		"path":         r.URL.Path,
		"query":        r.URL.Query(),
		"headers":      r.Header,
		"body_size":    len(body),
		"remote_addr":  getClientIP(r),
		"user_agent":   r.UserAgent(),
		"content_type": r.Header.Get("Content-Type"),
		"protocol":     r.Proto,
		"tls":          r.TLS != nil,
		"request_id":   r.Header.Get("X-Request-ID"),
		"server": map[string]interface{}{
			"hostname":      config.Hostname,
			"version":       "2.0.0",
			"go_version":    runtime.Version(),
			"platform":      runtime.GOOS + "/" + runtime.GOARCH,
			"start_time":    startTime,
			"uptime":        time.Since(startTime).String(),
			"request_count": requestCounter,
		},
	})
}

// WebSocket handler
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()
	log.Printf("WebSocket connected: %s", r.RemoteAddr)
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}
		if config.LogBody {
			log.Printf("%s | ws | %s", r.RemoteAddr, message)
		}
		err = conn.WriteMessage(messageType, message)
		if err != nil {
			log.Printf("WebSocket write error: %v", err)
			break
		}
	}
}

// Server-Sent Events handler
func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Configurable ticker interval (default 5 seconds)
	tickerIntervalStr := getEnv("ECHO_SSE_TICKER", "5s")
	tickerInterval, err := time.ParseDuration(tickerIntervalStr)
	if err != nil || tickerInterval <= 0 {
		tickerInterval = 5 * time.Second
	}
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			counter++
			data := map[string]interface{}{
				"counter":   counter,
				"timestamp": time.Now(),
				"uptime":    time.Since(startTime).String(),
			}
			jsonData, _ := json.Marshal(data)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			flusher.Flush()
		}
	}
}

// Frontend for WebSocket
func serveFrontendWS(w http.ResponseWriter, r *http.Request) {
	const templateName = "html/websocket.html"
	tmpl, err := template.ParseFS(files, templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templateData := struct {
		Host string
	}{
		Host: r.Host,
	}
	err = tmpl.Execute(w, templateData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "text/html")
	w.WriteHeader(200)
}

// Frontend for SSE
func serveFrontendSSE(w http.ResponseWriter, r *http.Request) {
	const templateName = "html/sse.html"
	tmpl, err := template.ParseFS(files, templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templateData := struct {
		Host string
	}{
		Host: r.Host,
	}
	err = tmpl.Execute(w, templateData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "text/html")
	w.WriteHeader(200)
}

// History handler
func historyHandler(w http.ResponseWriter, r *http.Request) {
	historyMutex.Lock()
	defer historyMutex.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(requestHistory)
}

// Replay handler
func replayHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Target string `json:"target"` // Optional external URL
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	historyMutex.Lock()
	found := false
	var record RequestRecord
	for _, rec := range requestHistory {
		if rec.ID == req.ID {
			record = rec
			found = true
			break
		}
	}
	historyMutex.Unlock()

	if !found {
		http.Error(w, "Request ID not found", http.StatusNotFound)
		return
	}

	if req.Target != "" {
		// Replay to external target
		client := &http.Client{Timeout: 30 * time.Second}
		httpReq, err := http.NewRequest(record.Method, req.Target, bytes.NewReader(record.Body))
		if err != nil {
			http.Error(w, "Failed to create request: "+err.Error(), http.StatusInternalServerError)
			return
		}
		httpReq.Header = record.Headers.Clone()
		resp, err := client.Do(httpReq)
		if err != nil {
			http.Error(w, "Replay failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Failed to read response body: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  resp.StatusCode,
			"body":    string(body),
			"headers": resp.Header,
		})
	} else {
		// Echo back the stored request as response
		contentType := record.Headers.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		w.Write(record.Body)
	}
}

// Scenario handler
func scenarioHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		var result []Scenario
		scenarios.Range(func(key, value interface{}) bool {
			path := key.(string)
			responses := value.([]Response)
			result = append(result, Scenario{Path: path, Responses: responses})
			return true
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	var scenariosData []Scenario
	if err := json.NewDecoder(r.Body).Decode(&scenariosData); err != nil {
		http.Error(w, "Invalid scenario data", http.StatusBadRequest)
		return
	}
	for _, s := range scenariosData {
		scenarios.Store(s.Path, s.Responses)
		scenarioIndex.Store(s.Path, 0)
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "scenarios updated"})
}

// Record request to history
func recordRequest(r *http.Request, body []byte) {
	historyMutex.Lock()
	defer historyMutex.Unlock()
	record := RequestRecord{
		ID:        r.Header.Get("X-Request-ID"),
		Timestamp: time.Now(),
		Method:    r.Method,
		URL:       r.RequestURI,
		Headers:   r.Header.Clone(),
		Body:      body,
	}
	requestHistory = append(requestHistory, record)
	if len(requestHistory) > config.HistorySize {
		requestHistory = requestHistory[1:]
	}
}

// Helper functions
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func getHeaderOrEnv(r *http.Request, header, env string) string {
	if val := r.Header.Get(header); val != "" {
		return val
	}
	return os.Getenv(env)
}

func generateRequestID() string {
	bytes := make([]byte, 8)
	if _, err := crand.Read(bytes); err != nil {
		panic("failed to generate request ID: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseInt64(s string) int64 {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	return 0
}

func parseFloat64(s string) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return 0
}

func generateSelfSignedCert() {
	privateKey, err := rsa.GenerateKey(crand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate private key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Advanced Echo Server"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(crand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		log.Fatalf("Failed to create certificate: %v", err)
	}
	certFile, err := os.Create(config.CertFile)
	if err != nil {
		log.Fatalf("Failed to create certificate file: %v", err)
	}
	defer certFile.Close()
	pemBytes := bytes.NewBuffer([]byte{})
	pemBytes.Write(derBytes)
	io.Copy(certFile, pemBytes)
	keyFile, err := os.Create(config.KeyFile)
	if err != nil {
		log.Fatalf("Failed to create key file: %v", err)
	}
	defer keyFile.Close()
	pemKey := bytes.NewBuffer([]byte{})
	pemKey.Write(x509.MarshalPKCS1PrivateKey(privateKey))
	io.Copy(keyFile, pemKey)
	log.Println("Successfully generated self-signed certificate and key.")
}

func main() {
	router := setupRoutes()

	// Wrap the router with h2c to support HTTP/2 over cleartext
	handler := h2c.NewHandler(router, &http2.Server{})

	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Remove date/time from log output
	log.SetFlags(0)
	log.Println(`
 ⢀⣀ ⢀⣸ ⡀⢀ ⢀⣀ ⣀⡀ ⢀⣀ ⢀⡀ ⢀⣸   ⢀⡀ ⢀⣀ ⣇⡀ ⢀⡀   ⢀⣀ ⢀⡀ ⡀⣀ ⡀⢀ ⢀⡀ ⡀⣀
 ⠣⠼ ⠣⠼ ⠱⠃ ⠣⠼ ⠇⠸ ⠣⠤ ⠣⠭ ⠣⠼   ⣇⠭ ⠣⠤ ⠇⠸ ⠣⠜   ⠭⠕ ⠣⠭ ⠏  ⠱⠃ ⠣⠭ ⠏ 
	`)
	log.SetFlags(log.LstdFlags)
	log.Printf("Advanced Echo Server starting on port %s", config.Port)

	var err error
	if config.EnableTLS {
		if _, err := os.Stat(config.CertFile); os.IsNotExist(err) {
			log.Println("Certificate file not found. Generating a self-signed certificate...")
			generateSelfSignedCert()
		}
		log.Printf("Starting HTTPS server with cert: %s", config.CertFile)
		err = server.ListenAndServeTLS(config.CertFile, config.KeyFile)
	} else {
		log.Printf("Starting HTTP server (with H2C support)")
		err = server.ListenAndServe()
	}

	if err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
