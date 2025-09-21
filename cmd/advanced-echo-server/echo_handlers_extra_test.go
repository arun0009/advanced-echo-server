package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContentTypePrecedence_Extra(t *testing.T) {
	setupTest()
	req, _ := http.NewRequest("POST", "/", strings.NewReader("abc"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Echo-Content-Type", "text/csv")
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if rr.Header().Get("Content-Type") != "text/csv" {
		t.Errorf("content-type precedence failed: got %q", rr.Header().Get("Content-Type"))
	}
}

func TestLatencyInjectionFixedAndRange_Extra(t *testing.T) {
	setupTest()
	// Fixed
	req1, _ := http.NewRequest("GET", "/", nil)
	req1.Header.Set("X-Echo-Latency", "75ms")
	rr1 := httptest.NewRecorder()
	start := time.Now()
	http.HandlerFunc(echoHandler).ServeHTTP(rr1, req1)
	d1 := time.Since(start)
	if rr1.Code != http.StatusOK {
		t.Fatalf("fixed latency status: %d", rr1.Code)
	}
	if d1 < 60*time.Millisecond || d1 > 140*time.Millisecond {
		t.Errorf("fixed latency duration unexpected: %v", d1)
	}
	// Range
	req2, _ := http.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Echo-Latency", "30ms-60ms")
	rr2 := httptest.NewRecorder()
	start2 := time.Now()
	http.HandlerFunc(echoHandler).ServeHTTP(rr2, req2)
	d2 := time.Since(start2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("range latency status: %d", rr2.Code)
	}
	if d2 < 20*time.Millisecond || d2 > 120*time.Millisecond {
		t.Errorf("range latency duration unexpected: %v", d2)
	}
}

func TestChaosErrorInjectionRandom_Extra(t *testing.T) {
	setupTest()
	withTestRNGSeed(1, func() {
		req, _ := http.NewRequest("GET", "/", nil)
		req.Header.Set("X-Echo-Chaos", "100") // always inject
		rr := httptest.NewRecorder()
		http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			t.Fatalf("expected chaos error status, got %d", rr.Code)
		}
		allowed := map[int]bool{500: true, 502: true, 503: true, 504: true, 408: true, 429: true}
		if !allowed[rr.Code] {
			t.Errorf("unexpected chaos status: %d", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/plain" {
			t.Errorf("unexpected content-type: %q", ct)
		}
		if !strings.HasPrefix(rr.Body.String(), "Chaos error injection:") {
			t.Errorf("unexpected body: %q", rr.Body.String())
		}
	})
}

func TestEchoCustomHeaders_ListAndServerInfo_Extra(t *testing.T) {
	setupTest()
	// Test X-Echo-Headers mirroring with X-Echoed- prefix
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("X-Echo-Headers", "X-Foo, X-Bar")
	req.Header.Set("X-Foo", "foo-val")
	req.Header.Set("X-Bar", "bar-val")
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if rr.Header().Get("X-Echoed-X-Foo") != "foo-val" || rr.Header().Get("X-Echoed-X-Bar") != "bar-val" {
		t.Errorf("echoed headers missing or wrong: %v", rr.Header())
	}
	// Test server info headers via env/header flag
	configLock.Lock()
	config.Hostname = "test-host"
	configLock.Unlock()
	req2, _ := http.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Echo-Server-Info", "true")
	rr2 := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr2, req2)
	if rr2.Header().Get("X-Echo-Server") == "" || rr2.Header().Get("X-Echo-Version") == "" || rr2.Header().Get("X-Echo-Uptime") == "" {
		t.Errorf("server info headers missing: %v", rr2.Header())
	}
}

func TestSetResponseContentType_Default_Extra(t *testing.T) {
	setupTest()
	req, _ := http.NewRequest("POST", "/", strings.NewReader("hello"))
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if rr.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("default Content-Type expected text/plain, got %q", rr.Header().Get("Content-Type"))
	}
}

func TestGetClientIP_Extra(t *testing.T) {
	setupTest()
	// X-Forwarded-For takes precedence
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	rr := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "Client-IP: 1.1.1.1") {
		t.Errorf("expected client ip 1.1.1.1, body=%q", rr.Body.String())
	}
	// X-Real-IP fallback
	req2, _ := http.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Real-IP", "3.3.3.3")
	rr2 := httptest.NewRecorder()
	http.HandlerFunc(echoHandler).ServeHTTP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), "Client-IP: 3.3.3.3") {
		t.Errorf("expected client ip 3.3.3.3, body=%q", rr2.Body.String())
	}
}
