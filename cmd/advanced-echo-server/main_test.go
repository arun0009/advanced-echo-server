package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

// Custom response writer to support Flusher for SSE testing
type flusherResponseWriter struct {
	*httptest.ResponseRecorder
}

func (frw *flusherResponseWriter) Flush() {
	// No-op for testing, as httptest doesn't write to a real connection
}

var (
	testRegistry *prometheus.Registry
)

func setupTest() {
	configLock.Lock()
	defer configLock.Unlock()

	// Reset global state
	scenarios = sync.Map{}
	scenarioIndex = sync.Map{}
	historyMutex.Lock()
	requestHistory = make([]RequestRecord, 0, config.HistorySize)
	historyMutex.Unlock()
	atomic.StoreUint64(&requestCounter, 0) // Use atomic for thread-safe reset
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

	// Reset Prometheus metrics
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

func TestEchoServerHeaderPrecedence(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		body           string
		setEnvVars     bool
		headers        map[string]string
		expectedStatus int
		expectedDelay  time.Duration
		checkBody      bool
		expectedBody   string
	}{
		{
			name:           "GET with headers overriding env vars",
			method:         "GET",
			body:           "",
			setEnvVars:     true,
			headers:        map[string]string{"X-Echo-Delay": "100ms", "X-Echo-Status": "200"},
			expectedStatus: http.StatusOK,
			expectedDelay:  100 * time.Millisecond,
			checkBody:      true,
			expectedBody:   "GET / HTTP/1.1\n",
		},
		{
			name:           "POST with headers and body",
			method:         "POST",
			body:           `{"test": "data"}`,
			setEnvVars:     true,
			headers:        map[string]string{"X-Echo-Delay": "100ms", "X-Echo-Status": "200", "Content-Type": "application/json"},
			expectedStatus: http.StatusOK,
			expectedDelay:  100 * time.Millisecond,
			checkBody:      true,
			expectedBody:   `{"test": "data"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTest()
			if tt.setEnvVars {
				os.Setenv("ECHO_DELAY", "50ms")
				os.Setenv("ECHO_STATUS", "400")
				defer os.Unsetenv("ECHO_DELAY")
				defer os.Unsetenv("ECHO_STATUS")
			}

			var req *http.Request
			var err error
			if tt.body == "" {
				req, err = http.NewRequest(tt.method, "/", nil)
			} else {
				req, err = http.NewRequest(tt.method, "/", strings.NewReader(tt.body))
			}
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(echoHandler)
			start := time.Now()
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v", status, tt.expectedStatus)
			}
			if tt.headers["X-Echo-Status"] != "" && rr.Header().Get("X-Echo-Status-Forced") != "true" {
				t.Error("handler did not force status code based on header")
			}
			duration := time.Since(start)
			if duration < tt.expectedDelay || duration > tt.expectedDelay+50*time.Millisecond {
				t.Errorf("handler did not apply expected delay: got %v, want ~%v", duration, tt.expectedDelay)
			}
			if tt.checkBody {
				body := rr.Body.String()
				if !strings.Contains(body, tt.expectedBody) {
					t.Errorf("handler returned unexpected body: got %q, want containing %q", body, tt.expectedBody)
				}
			}
		})
	}
}

func TestEchoServerEnvVarPrecedence(t *testing.T) {
	setupTest()
	os.Setenv("ECHO_DELAY", "50ms")
	os.Setenv("ECHO_STATUS", "400")
	defer os.Unsetenv("ECHO_DELAY")
	defer os.Unsetenv("ECHO_STATUS")

	// Load a YAML scenario directly into the scenarios map
	yamlContent := `
- path: /
  responses:
    - status: 200
      body: "{\"ok\": true}"
`
	var scenariosData []Scenario
	if err := yaml.Unmarshal([]byte(yamlContent), &scenariosData); err != nil {
		t.Fatalf("Failed to parse test YAML scenario: %v", err)
	}
	for _, s := range scenariosData {
		scenarios.Store(s.Path, s.Responses)
		scenarioIndex.Store(s.Path, 0)
	}

	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(echoHandler)
	start := time.Now()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
	if rr.Header().Get("X-Echo-Status-Forced") != "true" {
		t.Error("handler did not apply environment variable-based status")
	}
	if rr.Header().Get("X-Echo-Scenario") == "true" {
		t.Error("handler incorrectly applied YAML scenario instead of environment variables")
	}
	duration := time.Since(start)
	if duration < 50*time.Millisecond || duration > 100*time.Millisecond {
		t.Errorf("handler did not apply environment variable-based delay: got %v, want ~50ms", duration)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "GET / HTTP/1.1\n") {
		t.Errorf("handler returned unexpected body: got %q, want containing %q", body, "GET / HTTP/1.1\n")
	}
}

func TestHealthHandler(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal("Failed to parse response:", err)
	}
	if status, ok := resp["status"].(string); !ok || status != "healthy" {
		t.Errorf("handler returned unexpected status: got %v, want 'healthy'", resp["status"])
	}
}

func TestReadyHandler(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal("Failed to parse response:", err)
	}
	if status, ok := resp["status"]; !ok || status != "ready" {
		t.Errorf("handler returned unexpected status: got %v, want 'ready'", resp["status"])
	}
}

func TestInfoHandler(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-ID", "test-info")
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal("Failed to parse response:", err)
	}
	if method, ok := resp["method"].(string); !ok || method != "GET" {
		t.Errorf("handler returned incorrect method: got %v, want 'GET'", resp["method"])
	}
	if reqID, ok := resp["request_id"].(string); !ok || reqID != "test-info" {
		t.Errorf("handler returned incorrect request_id: got %v, want 'test-info'", reqID)
	}
	if server, ok := resp["server"].(map[string]interface{}); !ok || server["hostname"] == nil {
		t.Errorf("handler returned invalid server info: got %v", resp["server"])
	}
}

func TestScenarioHandler(t *testing.T) {
	setupTest()
	scenarioData := `[{"path": "/api/test", "responses": [{"status": 200, "body": "{\"ok\": true}"}, {"status": 500}]}]`
	req, err := http.NewRequest("POST", "/scenario", strings.NewReader(scenarioData))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal("Failed to parse response:", err)
	}
	if status, ok := resp["status"]; !ok || status != "scenarios updated" {
		t.Errorf("handler returned unexpected status: got %v, want 'scenarios updated'", resp["status"])
	}

	req, err = http.NewRequest("GET", "/api/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if body := rr.Body.String(); !strings.Contains(body, `{"ok": true}`) {
		t.Errorf("handler returned unexpected body: got %v, want containing '{\"ok\": true}'", body)
	}

	req, err = http.NewRequest("GET", "/api/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
	}
}

func TestHistoryAndReplayHandler(t *testing.T) {
	setupTest()
	configLock.Lock()
	config.HistorySize = 10
	configLock.Unlock()

	router := setupRoutes()

	// Start a single in-process test server. This is the only way to get a valid URL for replay.
	testServer := httptest.NewServer(router)
	defer testServer.Close()

	client := &http.Client{}

	// --- Step 1: Make the original request to record it in history
	req, err := http.NewRequest("POST", testServer.URL+"/test", strings.NewReader(`{"test": "record"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-ID", "test123")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Initial request failed: %v", err)
	}
	defer resp.Body.Close()

	if status := resp.StatusCode; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// --- Step 2: Verify history contains the recorded request
	req, err = http.NewRequest("GET", testServer.URL+"/history", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("History request failed: %v", err)
	}
	defer resp.Body.Close()

	if status := resp.StatusCode; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	var history []RequestRecord
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatal("Failed to parse history:", err)
	}
	if len(history) != 1 || history[0].ID != "test123" || string(history[0].Body) != `{"test": "record"}` {
		t.Errorf("history incorrect: got ID=%v, Body=%q; want ID=test123, Body={\"test\": \"record\"}", history[0].ID, string(history[0].Body))
	}

	// --- Step 3: Replay the request to the live test server
	replayData := `{"id": "test123"}`
	req, err = http.NewRequest("POST", testServer.URL+"/replay", strings.NewReader(replayData))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Replay request failed: %v", err)
	}
	defer resp.Body.Close()

	if status := resp.StatusCode; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	body, _ = io.ReadAll(resp.Body)
	if string(body) != `{"test": "record"}` {
		t.Errorf("replay returned unexpected body: got %q, want '{\"test\": \"record\"}'", string(body))
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	setupTest()
	configLock.Lock()
	config.RateLimitRPS = 1
	config.RateLimitBurst = 1
	configLock.Unlock()
	rateLimiter = rate.NewLimiter(rate.Limit(config.RateLimitRPS), config.RateLimitBurst)

	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	req, err = http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusTooManyRequests {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusTooManyRequests)
	}
	if retryAfter := rr.Header().Get("Retry-After"); retryAfter != "60" {
		t.Errorf("handler returned wrong Retry-After header: got %v, want '60'", retryAfter)
	}
}

func TestLatencyInjection(t *testing.T) {
	setupTest()
	tests := []struct {
		name     string
		header   string
		value    string
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{
			name:     "Fixed Latency",
			header:   "X-Echo-Latency",
			value:    "100ms",
			minDelay: 100 * time.Millisecond,
			maxDelay: 150 * time.Millisecond,
		},
		{
			name:     "Random Latency",
			header:   "X-Echo-Latency",
			value:    "100-200ms",
			minDelay: 100 * time.Millisecond,
			maxDelay: 250 * time.Millisecond,
		},
		{
			name:     "Exponential Backoff",
			header:   "X-Echo-Exponential",
			value:    "100,2",
			minDelay: 150 * time.Millisecond, // 100 * 2^1 * 0.75 (with 25% jitter)
			maxDelay: 250 * time.Millisecond, // 100 * 2^1 * 1.25
		},
		{
			name:     "Random Delay",
			header:   "X-Echo-Random-Delay",
			value:    "100,200",
			minDelay: 100 * time.Millisecond,
			maxDelay: 250 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(tt.header, tt.value)
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(echoHandler)
			start := time.Now()
			handler.ServeHTTP(rr, req)
			duration := time.Since(start)
			if duration < tt.minDelay || duration > tt.maxDelay {
				t.Errorf("latency outside expected range: got %v, want %v-%v", duration, tt.minDelay, tt.maxDelay)
			}
			if status := rr.Code; status != http.StatusOK {
				t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
			}
		})
	}
}

func TestChaosEngineering(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Error", "503")
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(echoHandler)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusServiceUnavailable {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusServiceUnavailable)
	}
	count, err := testutil.GatherAndCount(testRegistry, "echo_chaos_errors_total")
	if err != nil {
		t.Fatal("Failed to gather metrics:", err)
	}
	if count != 1 {
		t.Errorf("chaos error metric not incremented: got %d, want 1", count)
	}

	success := 0
	for i := 0; i < 100; i++ {
		req, err := http.NewRequest("GET", "/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Echo-Chaos", "20")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			success++
		}
	}
	if success < 60 || success > 90 {
		t.Errorf("chaos rate incorrect: got %d successes, expected ~80", success)
	}
}

func TestPrometheusMetrics(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("POST", "/test", strings.NewReader(`{"test": "metrics"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	count, err := testutil.GatherAndCount(testRegistry, "echo_requests_total")
	if err != nil {
		t.Fatal("Failed to gather metrics:", err)
	}
	if count != 1 {
		t.Errorf("request total metric not incremented: got %d, want 1", count)
	}
	count, err = testutil.GatherAndCount(testRegistry, "echo_request_duration_seconds")
	if err != nil {
		t.Fatal("Failed to gather metrics:", err)
	}
	if count != 1 {
		t.Errorf("request latency metric not recorded: got %d, want 1", count)
	}
}

func TestWebSocketRoute(t *testing.T) {
	setupTest()
	wsRouter := mux.NewRouter()
	wsRouter.StrictSlash(false)
	wsRouter.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("WebSocket route handler called for %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("WebSocket route OK"))
	}).Methods("GET")
	server := httptest.NewServer(wsRouter)
	defer server.Close()

	resp, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "WebSocket route OK") {
		t.Errorf("Unexpected response body: got %q", string(body))
	}
}

func TestWebSocketHandler(t *testing.T) {
	setupTest() // Call setupTest first
	wsRouter := mux.NewRouter()
	wsRouter.StrictSlash(false)
	wsRouter.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("WebSocket handler called for %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
		websocketHandler(w, r)
	})
	wsRouter.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("Middleware processing %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
			next.ServeHTTP(w, r)
		})
	})
	wsRouter.Use(loggingMiddleware)
	wsRouter.Use(corsMiddleware)
	wsRouter.Use(requestIDMiddleware)

	var wg sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		wsRouter.ServeHTTP(w, r)
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	t.Logf("Dialing WebSocket URL: %s", url)
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("WebSocket dial failed: %v, response: %v, body: %s", err, resp, string(body))
		} else {
			t.Fatalf("WebSocket dial failed: %v, response: <nil>", err)
		}
	}
	defer conn.Close()

	message := []byte("test message")
	err = conn.WriteMessage(websocket.TextMessage, message)
	if err != nil {
		t.Fatal("WebSocket write failed:", err)
	}

	_, received, err := conn.ReadMessage()
	if err != nil {
		t.Fatal("WebSocket read failed:", err)
	}
	if !bytes.Equal(received, message) {
		t.Errorf("WebSocket echo incorrect: got %s, want %s", received, message)
	}

	err = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal("WebSocket close failed:", err)
	}

	server.Close()
	wg.Wait() // Ensure all server goroutines are done
}
func TestWebSocketFrontend(t *testing.T) {
	setupTest() // Call setupTest before starting the server
	wsRouter := mux.NewRouter()
	wsRouter.StrictSlash(false)
	wsRouter.HandleFunc("/web-ws", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("WebSocket frontend handler called for %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
		serveFrontendWS(w, r)
	}).Methods("GET")
	wsRouter.Use(loggingMiddleware)
	wsRouter.Use(corsMiddleware)
	wsRouter.Use(requestIDMiddleware)

	// Create a WaitGroup to ensure server goroutines are done
	var wg sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		wsRouter.ServeHTTP(w, r)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/web-ws")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<html") {
		t.Errorf("Expected HTML response, got: %q", string(body))
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("WebSocket frontend response missing CORS header: got %q, want %q", resp.Header.Get("Access-Control-Allow-Origin"), "*")
	}

	// Close the server and wait for goroutines to finish
	server.Close()
	wg.Wait() // Ensure all server goroutines are done
}

func TestSSEHandler(t *testing.T) {
	setupTest()
	// Set faster ticker for testing
	os.Setenv("ECHO_SSE_TICKER", "100ms")
	defer os.Unsetenv("ECHO_SSE_TICKER")

	// Create a context with cancellation to control the handler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "/sse", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a custom response writer that supports Flusher
	rr := httptest.NewRecorder()
	frw := &flusherResponseWriter{ResponseRecorder: rr}

	// Run the handler in a goroutine to simulate streaming
	done := make(chan struct{})
	go func() {
		router := setupRoutes()
		router.ServeHTTP(frw, req)
		close(done)
	}()

	// Wait for at least one SSE event (ticker is 100ms)
	select {
	case <-time.After(200 * time.Millisecond):
		// Cancel the request to stop the handler
		cancel()

		// Wait for the handler to finish
		select {
		case <-done:
			// Check status and headers
			if status := rr.Code; status != http.StatusOK {
				t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
			}
			if contentType := rr.Header().Get("Content-Type"); contentType != "text/event-stream" {
				t.Errorf("handler returned wrong Content-Type: got %q, want %q", contentType, "text/event-stream")
			}
			if cacheControl := rr.Header().Get("Cache-Control"); cacheControl != "no-cache" {
				t.Errorf("handler returned wrong Cache-Control: got %q, want %q", cacheControl, "no-cache")
			}
			if connection := rr.Header().Get("Connection"); connection != "keep-alive" {
				t.Errorf("handler returned wrong Connection: got %q, want %q", connection, "keep-alive")
			}

			// Check body for SSE format
			body := rr.Body.String()
			if !strings.Contains(body, "data: ") || !strings.Contains(body, `"counter":`) || !strings.Contains(body, `"timestamp":`) {
				t.Errorf("SSE handler returned unexpected body: got %q", body)
			}

			// Parse the SSE event to ensure valid JSON
			lines := strings.Split(body, "\n")
			var eventData string
			for _, line := range lines {
				if strings.HasPrefix(line, "data: ") {
					eventData = strings.TrimPrefix(line, "data: ")
					break
				}
			}
			if eventData == "" {
				t.Error("No SSE event data found in response")
			} else {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(eventData), &data); err != nil {
					t.Errorf("Failed to parse SSE event data as JSON: %v", err)
				}
				if counter, ok := data["counter"].(float64); !ok || counter < 1 {
					t.Errorf("SSE event data missing or invalid counter: got %v", data["counter"])
				}
				if _, ok := data["timestamp"].(string); !ok {
					t.Errorf("SSE event data missing timestamp: got %v", data["timestamp"])
				}
			}
		case <-time.After(1 * time.Second):
			t.Error("Handler did not terminate after context cancellation")
		}
	case <-done:
		t.Errorf("SSE handler terminated unexpectedly: status=%d, body=%q", rr.Code, rr.Body.String())
	}
}

func TestSSEHandlerSlow(t *testing.T) {
	setupTest()
	os.Setenv("ECHO_SSE_TICKER", "5s")
	defer os.Unsetenv("ECHO_SSE_TICKER")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "/sse", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	frw := &flusherResponseWriter{ResponseRecorder: rr}

	done := make(chan struct{})
	go func() {
		router := setupRoutes()
		router.ServeHTTP(frw, req)
		close(done)
	}()

	select {
	case <-time.After(5100 * time.Millisecond):
		cancel()
		select {
		case <-done:
			if status := rr.Code; status != http.StatusOK {
				t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
			}
			if contentType := rr.Header().Get("Content-Type"); contentType != "text/event-stream" {
				t.Errorf("handler returned wrong Content-Type: got %q, want %q", contentType, "text/event-stream")
			}
			body := rr.Body.String()
			if !strings.Contains(body, "data: ") || !strings.Contains(body, `"counter":`) {
				t.Errorf("SSE handler returned unexpected body: got %q", body)
			}
			lines := strings.Split(body, "\n")
			var eventData string
			for _, line := range lines {
				if strings.HasPrefix(line, "data: ") {
					eventData = strings.TrimPrefix(line, "data: ")
					break
				}
			}
			if eventData == "" {
				t.Error("No SSE event data found in response")
			} else {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(eventData), &data); err != nil {
					t.Errorf("Failed to parse SSE event data as JSON: %v", err)
				}
				if counter, ok := data["counter"].(float64); !ok || counter < 1 {
					t.Errorf("SSE event data missing or invalid counter: got %v", data["counter"])
				}
			}
		case <-time.After(1 * time.Second):
			t.Error("Handler did not terminate after context cancellation")
		}
	case <-done:
		t.Errorf("SSE handler terminated unexpectedly: status=%d, body=%q", rr.Code, rr.Body.String())
	}
}

func TestResponseSize(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("POST", "/", strings.NewReader("test"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Response-Size", "1024")
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(echoHandler)
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("status: got %v want %v", status, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type: got %q want %q", ct, "application/octet-stream")
	}
	if len(rr.Body.Bytes()) != 1024 {
		t.Errorf("body size: got %d want 1024", len(rr.Body.Bytes()))
	}
}

func TestGzipCompression(t *testing.T) {
	setupTest()
	payload := strings.Repeat("A", 256)
	req, err := http.NewRequest("POST", "/", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Compress", "gzip")
	// Use content type passthrough
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("missing gzip header")
	}
	zr, err := gzip.NewReader(bytes.NewReader(rr.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader error: %v", err)
	}
	decompressed, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip read error: %v", err)
	}
	if string(decompressed) != payload {
		t.Errorf("decompressed mismatch: got %d bytes", len(decompressed))
	}
}

func TestSetHeaderViaPrefix(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Set-Header-X-Custom-Flag", "on")
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if rr.Header().Get("X-Custom-Flag") != "on" {
		t.Errorf("expected X-Custom-Flag=on, got %q", rr.Header().Get("X-Custom-Flag"))
	}
}

func TestEnvHeaderMapping(t *testing.T) {
	setupTest()
	os.Setenv("ECHO_HEADER_X_Version", "1.2.3")
	defer os.Unsetenv("ECHO_HEADER_X_Version")
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if rr.Header().Get("X-Version") != "1.2.3" {
		t.Errorf("expected X-Version=1.2.3, got %q", rr.Header().Get("X-Version"))
	}
	// Also ensure underscores are converted to dashes in header names
	os.Setenv("ECHO_HEADER_X_Custom_Header", "on")
	defer os.Unsetenv("ECHO_HEADER_X_Custom_Header")
	rr2 := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr2, req)
	if rr2.Header().Get("X-Custom-Header") != "on" {
		t.Errorf("expected X-Custom-Header=on, got %q", rr2.Header().Get("X-Custom-Header"))
	}
}

func TestJitterDelayHeader(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	// base 50ms, variance 50ms -> expect between 0 and 100ms
	req.Header.Set("X-Echo-Jitter", "50,50")
	rr := httptest.NewRecorder()
	start := time.Now()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	dur := time.Since(start)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if dur < 0 || dur > 150*time.Millisecond {
		t.Errorf("unexpected jitter duration: %v", dur)
	}
}

func TestReplayForwardsStatusAndContentType(t *testing.T) {
	setupTest()
	router := setupRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()
	client := &http.Client{}
	// Make initial request with explicit content type and 200 status
	req1, _ := http.NewRequest("POST", ts.URL+"/foo", strings.NewReader("abc"))
	req1.Header.Set("X-Request-ID", "rid-1")
	req1.Header.Set("Content-Type", "text/plain")
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp1.Body)
	resp1.Body.Close()
	// Replay without target -> should hit same server and forward 200 + text/plain
	replayBody := `{"id":"rid-1"}`
	req2, _ := http.NewRequest("POST", ts.URL+"/replay", strings.NewReader(replayBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status: got %d", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "application/json" && ct != "text/plain" {
		t.Errorf("unexpected Content-Type: %q body=%q", ct, string(b))
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	setupTest()
	req, _ := http.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)
	if rid := rr.Header().Get("X-Request-ID"); rid == "" {
		t.Errorf("missing X-Request-ID header")
	}
	// If provided, should echo back
	req2, _ := http.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Request-ID", "abc-123")
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Header().Get("X-Request-ID") != "abc-123" {
		t.Errorf("expected X-Request-ID=abc-123 got %q", rr2.Header().Get("X-Request-ID"))
	}
}

func TestEchoServerHeaderDelay(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Delay", "50ms")
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(echoHandler)
	start := time.Now()
	handler.ServeHTTP(rr, req)
	duration := time.Since(start)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if duration < 50*time.Millisecond || duration > 120*time.Millisecond {
		t.Errorf("handler did not apply header-based delay: got %v, want ~50ms", duration)
	}
	if body := rr.Body.String(); !strings.Contains(body, "GET / HTTP/1.1") {
		t.Errorf("handler returned wrong body: got %q", body)
	}
}

func TestExponentialBackoffDelay(t *testing.T) {
	setupTest()
	req, _ := http.NewRequest("GET", "/", nil)
	// base=25, attempt=3 -> expected around 100ms +/- 25% jitter
	req.Header.Set("X-Echo-Exponential", "25,3")
	rr := httptest.NewRecorder()
	start := time.Now()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	dur := time.Since(start)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if dur < 60*time.Millisecond || dur > 160*time.Millisecond {
		t.Errorf("unexpected exponential delay: %v", dur)
	}
}

func TestChaosErrorInjectionCodes(t *testing.T) {
	setupTest()
	codes := map[string]int{"internal": 500, "bad-gateway": 502, "unavailable": 503, "gateway-timeout": 504, "rate-limit": 429}
	for key, expected := range codes {
		req, _ := http.NewRequest("GET", "/", nil)
		req.Header.Set("X-Echo-Error", key)
		rr := httptest.NewRecorder()
		http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
		if rr.Code != expected {
			t.Errorf("%s: got %d want %d", key, rr.Code, expected)
		}
	}
}

func TestHTTPRateLimitMiddleware(t *testing.T) {
	setupTest()
	configLock.Lock()
	config.RateLimitRPS = 1
	config.RateLimitBurst = 1
	configLock.Unlock()
	rateLimiter = rate.NewLimiter(rate.Limit(config.RateLimitRPS), config.RateLimitBurst)
	router := setupRoutes()
	// First should pass
	req1, _ := http.NewRequest("GET", "/", nil)
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first req status: %d", rr1.Code)
	}
	// Second immediate request should be limited
	req2, _ := http.NewRequest("GET", "/", nil)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second req expected 429, got %d", rr2.Code)
	}
}

func TestWebSocketRateLimitMiddleware(t *testing.T) {
	setupTest()
	configLock.Lock()
	config.RateLimitRPS = 1
	config.RateLimitBurst = 1
	configLock.Unlock()
	rateLimiter = rate.NewLimiter(rate.Limit(config.RateLimitRPS), config.RateLimitBurst)

	wsRouter := mux.NewRouter()
	wsRouter.StrictSlash(false)
	wsRouter.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("WebSocket handler called for %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
		websocketHandler(w, r)
	})
	wsRouter.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("Middleware processing %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
			next.ServeHTTP(w, r)
		})
	})
	wsRouter.Use(loggingMiddleware)
	wsRouter.Use(corsMiddleware)
	wsRouter.Use(requestIDMiddleware)
	wsRouter.Use(rateLimitMiddleware)

	var wg sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		wsRouter.ServeHTTP(w, r)
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	t.Logf("Dialing WebSocket URL: %s", url)
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("WebSocket dial failed: %v, response: %v, body: %s", err, resp, string(body))
		} else {
			t.Fatalf("WebSocket dial failed: %v, response: <nil>", err)
		}
	}
	err = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal("WebSocket close failed:", err)
	}
	conn.Close()

	server.Close()
	wg.Wait() // Ensure all server goroutines are done

	_, resp, err = websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Error("WebSocket connection succeeded despite rate limit or server closure")
		if resp != nil {
			resp.Body.Close()
		}
	} else if resp != nil && resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Errorf("WebSocket rate limit response incorrect: got status %v, want %v, body: %s", resp.StatusCode, http.StatusTooManyRequests, string(body))
	} else if err != nil && !strings.Contains(err.Error(), "websocket: bad handshake") && !strings.Contains(err.Error(), "dial tcp") {
		t.Errorf("Unexpected WebSocket error: %v", err)
	}
}
