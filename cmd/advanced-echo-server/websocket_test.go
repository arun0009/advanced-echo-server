package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
)

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
	setupTest()
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
	wg.Wait()
}

func TestWebSocketFrontend(t *testing.T) {
	setupTest()
	wsRouter := mux.NewRouter()
	wsRouter.StrictSlash(false)
	wsRouter.HandleFunc("/web-ws", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("WebSocket frontend handler called for %s %s, headers: %v", r.Method, r.URL.Path, r.Header)
		serveFrontendWS(w, r)
	}).Methods("GET")
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

	server.Close()
	wg.Wait()
}

func TestWebSocketRateLimitMiddleware(t *testing.T) {
	setupTest()
	configLock.Lock()
	config.RateLimitRPS = 1
	config.RateLimitBurst = 1
	configLock.Unlock()
	// Initialize global limiter to match middleware behavior
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
	wg.Wait()

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
