package main

import (
	crand "crypto/rand"
	"embed"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/time/rate"

	mrand "math/rand"
)

//go:embed html/*
var files embed.FS

// RequestRecord stores request details for history/replay
type RequestRecord struct {
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Method    string      `json:"method"`
	URL       string      `json:"url"`
	Headers   http.Header `json:"headers"`
	Body      []byte      `json:"body"`
}

// Global state for metrics, counters, and scenarios
var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	rng            = mrand.New(mrand.NewSource(time.Now().UnixNano()))
	requestCounter uint64
	counterMutex   sync.Mutex
	scenarios      sync.Map
	scenarioIndex  sync.Map
	requestHistory []RequestRecord
	historyMutex   sync.Mutex
	rateLimiter    *rate.Limiter
)

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

// WebSocket handler
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("WebSocket handler invoked for %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("WebSocket close error: %v", err)
		} else {
			log.Printf("WebSocket connection closed: %s", r.RemoteAddr)
		}
	}()

	configLock.RLock()
	logBody := config.LogBody
	configLock.RUnlock()

	log.Printf("WebSocket connected: %s, subprotocol: %s", r.RemoteAddr, conn.Subprotocol())
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}
		if logBody {
			log.Printf("%s | ws | %s", r.RemoteAddr, string(message))
		}
		if err := conn.WriteMessage(messageType, message); err != nil {
			log.Printf("WebSocket write error: %v", err)
			break
		}
	}
	log.Printf("WebSocket loop exited: %s", r.RemoteAddr)
}

// Frontend for WebSocket
func serveFrontendWS(w http.ResponseWriter, r *http.Request) {
	data, err := files.ReadFile("html/websocket.html")
	if err != nil {
		http.Error(w, "Failed to read websocket.html: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func main() {
	initializeServer()
	router := setupRoutes()

	// Separate router for WebSocket endpoints (no H2C upgrade)
	wsRouter := mux.NewRouter()
	wsRouter.HandleFunc("/ws", websocketHandler)
	wsRouter.HandleFunc("/web-ws", serveFrontendWS)
	wsRouter.Use(loggingMiddleware)
	wsRouter.Use(corsMiddleware)
	wsRouter.Use(requestIDMiddleware)
	if rateLimiter != nil {
		wsRouter.Use(rateLimitMiddleware)
	}

	mixedRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" || r.URL.Path == "/web-ws" {
			wsRouter.ServeHTTP(w, r)
		} else {
			h2c.NewHandler(router, &http2.Server{}).ServeHTTP(w, r)
		}
	})

	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mixedRouter,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Disable WriteTimeout for SSE
		IdleTimeout:  0, // Disable IdleTimeout for SSE
	}

	log.SetFlags(0)
	log.Println(`
	 ⢀⣀ ⢀⣸ ⡀⢀ ⢀⣀ ⣀⡀ ⢀⣀ ⢀⡀ ⢀⣸   ⢀⡀ ⢀⣀ ⣇⡀ ⢀⡀   ⢀⣀ ⢀⡀ ⡀⣀ ⡀⢀ ⢀⡀ ⡀⣀
	 ⠣⠼ ⠣⠼ ⠱⠃ ⠣⠼ ⠇⠸ ⠣⠤ ⠣⠭ ⠣⠼   ⣇⠭ ⠣⠤ ⠇⠸ ⠣⠜   ⠭⠕ ⠣⠭ ⠏  ⠱⠃ ⠣⠭ ⠏ 
	   `)
	log.SetFlags(log.LstdFlags)
	log.Printf("Advanced Echo Server starting on port %s", config.Port)

	if err := startServer(server); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
