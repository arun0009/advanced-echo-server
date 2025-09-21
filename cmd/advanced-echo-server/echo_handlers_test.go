package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"gopkg.in/yaml.v3"
)

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
	if dur < 50*time.Millisecond || dur > 180*time.Millisecond {
		t.Errorf("unexpected exponential delay: %v", dur)
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
		{name: "Fixed Latency", header: "X-Echo-Latency", value: "100ms", minDelay: 90 * time.Millisecond, maxDelay: 180 * time.Millisecond},
		{name: "Random Latency", header: "X-Echo-Latency", value: "100-200ms", minDelay: 100 * time.Millisecond, maxDelay: 250 * time.Millisecond},
		{name: "Exponential Backoff", header: "X-Echo-Exponential", value: "100,2", minDelay: 150 * time.Millisecond, maxDelay: 250 * time.Millisecond},
		{name: "Random Delay", header: "X-Echo-Random-Delay", value: "100,200", minDelay: 100 * time.Millisecond, maxDelay: 250 * time.Millisecond},
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

	// Probabilistic chaos
	success := 0
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequest("GET", "/", nil)
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

func TestResponseSize(t *testing.T) {
	setupTest()
	req, err := http.NewRequest("POST", "/", strings.NewReader("test"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Response-Size", "1024")
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
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
