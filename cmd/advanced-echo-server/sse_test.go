package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

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
