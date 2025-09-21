package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScenarioRollover_Extra(t *testing.T) {
	setupTest()
	router := setupRoutes()
	scenarioData := `[{"path":"/roll","responses":[{"status":201,"body":"one"},{"status":202,"body":"two"}]}]`
	req, _ := http.NewRequest("POST", "/scenario", strings.NewReader(scenarioData))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("scenario post status: %d", rr.Code)
	}
	// First
	req1, _ := http.NewRequest("GET", "/roll", nil)
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	if rr1.Code != 201 {
		t.Fatalf("first resp code: %d", rr1.Code)
	}
	if rr1.Header().Get("X-Echo-Scenario") != "true" {
		t.Errorf("missing scenario header on first")
	}
	if !strings.Contains(rr1.Body.String(), "one") {
		t.Errorf("first body mismatch: %q", rr1.Body.String())
	}
	// Second
	req2, _ := http.NewRequest("GET", "/roll", nil)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code != 202 {
		t.Fatalf("second resp code: %d", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "two") {
		t.Errorf("second body mismatch: %q", rr2.Body.String())
	}
	// Third wraps to first
	req3, _ := http.NewRequest("GET", "/roll", nil)
	rr3 := httptest.NewRecorder()
	router.ServeHTTP(rr3, req3)
	if rr3.Code != 201 {
		t.Fatalf("third resp code: %d (expected rollover)", rr3.Code)
	}
}

func TestProcessScenario_DelayRange_And_EmptyBody_Extra(t *testing.T) {
	setupTest()
	// Install a scenario with range delay and empty body to trigger echoRequestInfo
	router := setupRoutes()
	scenario := `[{"path":"/sc-range-empty","responses":[{"status":200,"body":"","delay":"10-20ms"}]}]`
	req, _ := http.NewRequest("POST", "/scenario", strings.NewReader(scenario))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("scenario post status: %d", rr.Code)
	}

	// Call the scenario and measure duration
	req2, _ := http.NewRequest("GET", "/sc-range-empty", nil)
	rr2 := httptest.NewRecorder()
	start := time.Now()
	router.ServeHTTP(rr2, req2)
	dur := time.Since(start)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr2.Code)
	}
	if rr2.Header().Get("X-Echo-Scenario") != "true" {
		t.Errorf("missing X-Echo-Scenario header")
	}
	if ct := rr2.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("unexpected content-type: %q", ct)
	}
	if !strings.HasPrefix(rr2.Body.String(), "GET ") {
		t.Errorf("expected echoRequestInfo fallback, got: %q", rr2.Body.String())
	}
	if dur < 5*time.Millisecond || dur > 100*time.Millisecond {
		t.Errorf("delay out of expected range: %v", dur)
	}
}

func TestScenarioHandler_GetListsScenarios_Extra(t *testing.T) {
	setupTest()
	router := setupRoutes()
	payload := `[
		{"path":"/sa","responses":[{"status":200,"body":"A"}]},
		{"path":"/sb","responses":[{"status":201,"body":"B"}]}
	]`
	req, _ := http.NewRequest("POST", "/scenario", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("post scenarios failed: %d", rr.Code)
	}
	// GET and verify list includes both
	rr2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/scenario", nil)
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get scenarios failed: %d", rr2.Code)
	}
	var list []Scenario
	if err := json.Unmarshal(rr2.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to decode scenarios: %v", err)
	}
	foundA, foundB := false, false
	for _, s := range list {
		if s.Path == "/sa" && len(s.Responses) == 1 && s.Responses[0].Status == 200 {
			foundA = true
		}
		if s.Path == "/sb" && len(s.Responses) == 1 && s.Responses[0].Status == 201 {
			foundB = true
		}
	}
	if !(foundA && foundB) {
		t.Fatalf("expected both scenarios present, got: %+v", list)
	}
}

func TestScenarioHandler_BadJSON_Extra(t *testing.T) {
	setupTest()
	router := setupRoutes()
	rr := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/scenario", strings.NewReader("{"))
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad JSON, got %d", rr.Code)
	}
}

func TestScenarioHandler_IndexResetOnPost_Extra(t *testing.T) {
	setupTest()
	router := setupRoutes()
	// Install initial scenario with two responses
	payload1 := `[{"path":"/idx","responses":[{"status":200,"body":"r1"},{"status":201,"body":"r2"}]}]`
	rr := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/scenario", strings.NewReader(payload1))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("post scenarios failed: %d", rr.Code)
	}
	// Consume two responses to advance index
	for _, want := range []int{200, 201} {
		rrG := httptest.NewRecorder()
		reqG, _ := http.NewRequest("GET", "/idx", nil)
		router.ServeHTTP(rrG, reqG)
		if rrG.Code != want {
			t.Fatalf("expected %d, got %d", want, rrG.Code)
		}
	}
	// Post a new scenario for same path with a single response; index should reset
	payload2 := `[{"path":"/idx","responses":[{"status":500,"body":"n1"}]}]`
	rr2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/scenario", strings.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("post scenarios (update) failed: %d", rr2.Code)
	}
	// Next GET should return the new first response (500) if index reset worked
	rrG3 := httptest.NewRecorder()
	reqG3, _ := http.NewRequest("GET", "/idx", nil)
	router.ServeHTTP(rrG3, reqG3)
	if rrG3.Code != 500 {
		t.Fatalf("expected 500 after reset, got %d", rrG3.Code)
	}
}
