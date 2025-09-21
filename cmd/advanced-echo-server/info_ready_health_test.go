package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
