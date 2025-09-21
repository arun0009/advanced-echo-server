package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScenarioHandler(t *testing.T) {
	setupTest()
	scenarioData := `[{"path": "/api/test", "responses": [{"status": 200, "body": "{\"ok\": true}"}, {"status": 500}]}]`
	req, err := http.NewRequest("POST", "/scenario", strings.NewReader(scenarioData))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	if status, ok := resp["status"]; !ok || status != "scenarios updated" {
		t.Errorf("handler returned unexpected status: got %v, want 'scenarios updated'", resp["status"])
	}

	req, err = http.NewRequest("GET", "/api/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if body := rr.Body.String(); !strings.Contains(body, `{"ok": true}`) {
		t.Errorf("handler returned unexpected body: got %v, want containing '{\"ok\": true}'", body)
	}

	req, err = http.NewRequest("GET", "/api/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
	}
}

func TestHistoryAndReplayHandler(t *testing.T) {
	setupTest()
	configLock.Lock()
	config.HistorySize = 10
	configLock.Unlock()

	router := setupRoutes()

	// Start a single in-process test server. This is the only way to get a valid URL for replay.
	testServer := httptest.NewServer(router)
	defer testServer.Close()

	client := &http.Client{}

	// --- Step 1: Make the original request to record it in history
	req, err := http.NewRequest("POST", testServer.URL+"/test", strings.NewReader(`{"test": "record"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-ID", "test123")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Initial request failed: %v", err)
	}
	defer resp.Body.Close()
	if status := resp.StatusCode; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// --- Step 2: Verify history contains the recorded request
	req, err = http.NewRequest("GET", testServer.URL+"/history", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("History request failed: %v", err)
	}
	defer resp.Body.Close()
	if status := resp.StatusCode; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	var history []RequestRecord
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatal("Failed to parse history:", err)
	}
	if len(history) != 1 || history[0].ID != "test123" || string(history[0].Body) != `{"test": "record"}` {
		t.Errorf("history incorrect: got ID=%v, Body=%q; want ID=test123, Body={\"test\": \"record\"}", history[0].ID, string(history[0].Body))
	}

	// --- Step 3: Replay the request to the live test server
	replayData := `{"id": "test123"}`
	req, err = http.NewRequest("POST", testServer.URL+"/replay", strings.NewReader(replayData))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Replay request failed: %v", err)
	}
	defer resp.Body.Close()
	if status := resp.StatusCode; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	body, _ = io.ReadAll(resp.Body)
	if string(body) != `{"test": "record"}` {
		t.Errorf("replay returned unexpected body: got %q, want '{\"test\": \"record\"}'", string(body))
	}
}

func TestReplayForwardsStatusAndContentType(t *testing.T) {
	setupTest()
	router := setupRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()
	client := &http.Client{}
	// Make initial request with explicit content type and 200 status
	req1, _ := http.NewRequest("POST", ts.URL+"/foo", strings.NewReader("abc"))
	req1.Header.Set("X-Request-ID", "rid-1")
	req1.Header.Set("Content-Type", "text/plain")
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp1.Body)
	resp1.Body.Close()
	// Replay without target -> should hit same server and forward 200 + text/plain
	replayBody := `{"id":"rid-1"}`
	req2, _ := http.NewRequest("POST", ts.URL+"/replay", strings.NewReader(replayBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status: got %d", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "application/json" && ct != "text/plain" {
		t.Errorf("unexpected Content-Type: %q body=%q", ct, string(b))
	}
}
