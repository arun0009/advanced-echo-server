package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestEchoServerEnvVarPrecedence(t *testing.T) {
	// Set an environment variable
	os.Setenv("ECHO_DELAY", "50")
	os.Setenv("ECHO_STATUS", "400")

	// Create a request with conflicting headers
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Delay", "1000")
	req.Header.Set("X-Echo-Status", "200")

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(echoHandler)

	handler.ServeHTTP(rr, req)

	// Check that the status code from the environment variable was used
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}

	// Check that the delay was applied (this is a bit tricky to test directly,
	// but we can test the precedence of the status code, which uses the same logic).
	// The key here is to verify that the env var takes precedence.
	if rr.Header().Get("X-Echo-Status-Forced") != "true" {
		t.Error("handler did not force status code based on environment variable")
	}

	// Clean up environment variables
	os.Unsetenv("ECHO_DELAY")
	os.Unsetenv("ECHO_STATUS")
}

func TestEchoServerHeaderPrecedence(t *testing.T) {
	// Ensure no environment variables are set
	os.Unsetenv("ECHO_DELAY")
	os.Unsetenv("ECHO_STATUS")

	// Create a request with headers
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Echo-Delay", "1000")
	req.Header.Set("X-Echo-Status", "400")

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(echoHandler)

	handler.ServeHTTP(rr, req)

	// Check that the status code from the header was used
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}

	if rr.Header().Get("X-Echo-Status-Forced") != "true" {
		t.Error("handler did not force status code based on header")
	}
}

func TestHealthHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	router := setupRoutes()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	expected := `{"status":"healthy","timestamp":`
	if !strings.HasPrefix(rr.Body.String(), expected) {
		t.Errorf("handler returned unexpected body: got %v", rr.Body.String())
	}
}
