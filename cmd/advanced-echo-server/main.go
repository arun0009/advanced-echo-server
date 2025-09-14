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
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Config holds server configuration
type Config struct {
	Port        string
	EnableTLS   bool
	CertFile    string
	KeyFile     string
	EnableCORS  bool
	LogRequests bool
	LogHeaders  bool
	LogBody     bool
	MaxBodySize int64
	Hostname    string
}

//go:embed html/*
var files embed.FS

// Global state for metrics and counters
var (
	config    Config
	startTime time.Time
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	rng            = rand.New(rand.NewSource(time.Now().UnixNano()))
	requestCounter uint64
	counterMutex   sync.Mutex
)

func init() {
	startTime = time.Now()

	// Load configuration from environment variables
	config = Config{
		Port:        getEnv("PORT", "8080"),
		EnableTLS:   getEnv("ENABLE_TLS", "false") == "true",
		CertFile:    getEnv("CERT_FILE", "server.crt"),
		KeyFile:     getEnv("KEY_FILE", "server.key"),
		EnableCORS:  getEnv("ENABLE_CORS", "true") == "true",
		LogRequests: getEnv("LOG_REQUESTS", "true") == "true",
		LogHeaders:  getEnv("LOG_HEADERS", "false") == "true",
		LogBody:     getEnv("LOG_BODY", "false") == "true",
		MaxBodySize: parseInt64(getEnv("MAX_BODY_SIZE", "10485760")), // 10MB default
	}

	hostname, _ := os.Hostname()
	config.Hostname = hostname
}

func setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Apply middleware
	router.Use(loggingMiddleware)
	router.Use(corsMiddleware)
	router.Use(requestIDMiddleware)

	// Health check endpoints
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/ready", readyHandler).Methods("GET")

	// WebSocket and SSE endpoints
	router.HandleFunc("/ws", websocketHandler)
	router.HandleFunc("/sse", sseHandler)

	// Embedded frontend for WebSocket and SSE
	router.HandleFunc("/web-ws", serveFrontendWS)
	router.HandleFunc("/web-sse", serveFrontendSSE)

	// Request inspection (returns metadata, not echo)
	router.HandleFunc("/inspect", inspectHandler)

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

		next.ServeHTTP(w, r)

		if config.LogRequests {
			log.Printf("%s %s %s - %v", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
		}
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
		requestID := generateRequestID()
		r.Header.Set("X-Request-ID", requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r)
	})
}

// Main Echo Handler
func echoHandler(w http.ResponseWriter, r *http.Request) {
	// Increment request counter
	counterMutex.Lock()
	requestCounter++
	currentCount := requestCounter
	counterMutex.Unlock()

	// Process testing features based on headers first, then environment variables
	processTestingFeatures(w, r)

	// If a status was already set, return
	if w.Header().Get("X-Echo-Status-Forced") == "true" {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, config.MaxBodySize))
	if err != nil {
		http.Error(w, "Error reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply delays based on headers or env vars
	applyDelays(r)

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

// Process testing features based on headers first, then environment variables
func processTestingFeatures(w http.ResponseWriter, r *http.Request) {
	// Check headers first for all features
	// If not present, fall back to environment variables

	// Force HTTP status code
	statusStr := getHeaderOrEnv(r, "X-Echo-Status", "ECHO_STATUS")
	if statusStr != "" {
		if status, err := strconv.Atoi(statusStr); err == nil && status >= 100 && status <= 599 {
			w.Header().Set("X-Echo-Status-Forced", "true")
			w.WriteHeader(status)
			return
		}
	}

	// Force specific errors
	errorStr := getHeaderOrEnv(r, "X-Echo-Error", "ECHO_ERROR")
	if errorStr != "" {
		switch strings.ToLower(errorStr) {
		case "timeout":
			time.Sleep(65 * time.Second)
			return
		case "500", "internal":
			http.Error(w, "Simulated internal server error", http.StatusInternalServerError)
			w.Header().Set("X-Echo-Status-Forced", "true")
			return
		case "502", "bad-gateway":
			http.Error(w, "Simulated bad gateway", http.StatusBadGateway)
			w.Header().Set("X-Echo-Status-Forced", "true")
			return
		case "503", "unavailable":
			http.Error(w, "Simulated service unavailable", http.StatusServiceUnavailable)
			w.Header().Set("X-Echo-Status-Forced", "true")
			return
		case "504", "gateway-timeout":
			http.Error(w, "Simulated gateway timeout", http.StatusGatewayTimeout)
			w.Header().Set("X-Echo-Status-Forced", "true")
			return
		case "429", "rate-limit":
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Simulated rate limit exceeded", http.StatusTooManyRequests)
			w.Header().Set("X-Echo-Status-Forced", "true")
			return
		case "random":
			errors := []int{500, 502, 503, 504, 429}
			status := errors[rng.Intn(len(errors))]
			http.Error(w, fmt.Sprintf("Random simulated error: %d", status), status)
			w.Header().Set("X-Echo-Status-Forced", "true")
			return
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
				http.Error(w, fmt.Sprintf("Chaos error injection: %d", status), status)
				w.Header().Set("X-Echo-Status-Forced", "true")
				return
			}
		}
	}
}

// Apply various delay patterns
func applyDelays(r *http.Request) {
	delayStr := getHeaderOrEnv(r, "X-Echo-Delay", "ECHO_DELAY")
	if delayStr != "" {
		if delayMs, err := strconv.Atoi(delayStr); err == nil && delayMs > 0 {
			maxDelay := 300000 // 5 minutes max
			if delayMs > maxDelay {
				delayMs = maxDelay
			}
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
			return
		}
	}

	// Jitter delay: base±variance (in milliseconds)
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

	// Random delay between min,max (in milliseconds)
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

	// Exponential backoff simulation
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
}

// Echo back custom headers as requested
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

// Set appropriate content type for response
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
	response.WriteString(fmt.Sprintf("%s %s %s\n", r.Method, r.RequestURI, r.Proto))
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

// WebSocket handler
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()
	log.Printf("🔌 WebSocket connected: %s", r.RemoteAddr)
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
	ticker := time.NewTicker(5 * time.Second)
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

// Request inspection handler (returns metadata, not pure echo)
func inspectHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, config.MaxBodySize))
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
	if val := os.Getenv(env); val != "" {
		return val
	}
	return strings.TrimSpace(r.Header.Get(header))
}

func generateRequestID() string {
	bytes := make([]byte, 8)
	// Use crypto/rand for secure random bytes (fixes deprecation)
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
 ⠣⠼ ⠣⠼ ⠱⠃ ⠣⠼ ⠇⠸ ⠣⠤ ⠣⠭ ⠣⠼   ⠣⠭ ⠣⠤ ⠇⠸ ⠣⠜   ⠭⠕ ⠣⠭ ⠏  ⠱⠃ ⠣⠭ ⠏ 
	`)
	log.SetFlags(1)
	log.Printf("Advanced Echo Server starting on port %s", config.Port)

	var err error
	if config.EnableTLS {
		// Generate cert if files don't exist
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
