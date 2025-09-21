package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeFrontendSSE_Extra(t *testing.T) {
	setupTest()
	router := setupRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web-sse")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "<html") {
		t.Errorf("Expected HTML response, got: %q", string(b))
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
}
