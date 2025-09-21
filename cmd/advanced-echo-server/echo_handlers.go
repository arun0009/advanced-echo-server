package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"math"
	mrand "math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Main Echo Handler (moved from main.go)
func echoHandler(w http.ResponseWriter, r *http.Request) {
	// Increment request counter
	counterMutex.Lock()
	requestCounter++
	currentCount := requestCounter
	counterMutex.Unlock()

	// Read config fields with lock
	configLock.RLock()
	maxBodySize := config.MaxBodySize
	historySize := config.HistorySize
	configLock.RUnlock()

	// Record request for history
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, maxBodySize))
		if err != nil {
			http.Error(w, "Error reading body: "+err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	} else {
		body = []byte{}
	}
	if historySize > 0 {
		recordRequest(r, body)
	}

	// Apply delays from headers or environment variables
	applyDelays(r)

	// Process testing features
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
				mrand.Read(responseBody)
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

// Process testing features (moved from main.go)
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

// Apply various delay patterns (moved from main.go)
func applyDelays(r *http.Request) {
	// Simple delay
	delayStr := getHeaderOrEnv(r, "X-Echo-Delay", "ECHO_DELAY")
	if delayStr != "" {
		delayStr = strings.TrimSuffix(delayStr, "ms")
		if delayMs, err := strconv.Atoi(delayStr); err == nil && delayMs > 0 {
			maxDelay := 300000
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

// Echo back custom headers (moved from main.go)
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
		configLock.RLock()
		hostname := config.Hostname
		configLock.RUnlock()
		w.Header().Set("X-Echo-Server", hostname)
		w.Header().Set("X-Echo-Version", "1.0.0")
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

// Set appropriate content type (moved from main.go)
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

// Echo request information for GET requests without body (moved from main.go)
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
