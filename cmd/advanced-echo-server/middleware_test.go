package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

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
