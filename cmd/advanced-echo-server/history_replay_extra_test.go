package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReplayHandler_InvalidAndNotFound_Extra(t *testing.T) {
	setupTest()
	router := setupRoutes()
	// Invalid JSON
	rr := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/replay", strings.NewReader("{"))
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON expected 400, got %d", rr.Code)
	}
	// Not found ID
	// First, record a request so history machinery is initialized
	rrInit := httptest.NewRecorder()
	reqInit, _ := http.NewRequest("GET", "/any", nil)
	reqInit.Header.Set("X-Request-ID", "some-id")
	router.ServeHTTP(rrInit, reqInit)
	// Now request replay for a non-existent id
	rr2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/replay", strings.NewReader(`{"id":"missing"}`))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("replay missing id expected 404, got %d", rr2.Code)
	}
}
