package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// History handler
func historyHandler(w http.ResponseWriter, r *http.Request) {
	historyMutex.Lock()
	defer historyMutex.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(requestHistory)
}

// Replay handler
func replayHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	historyMutex.Lock()
	found := false
	var record RequestRecord
	for _, rec := range requestHistory {
		if rec.ID == req.ID {
			record = rec
			found = true
			break
		}
	}
	historyMutex.Unlock()

	if !found {
		http.Error(w, "Request ID not found", http.StatusNotFound)
		return
	}

	// Use the host and scheme from the incoming request to create a dynamic URL.
	schema := "http"
	if r.TLS != nil {
		schema = "https"
	}
	r.URL.Scheme = schema
	targetURL := req.Target
	if targetURL == "" {
		targetURL = fmt.Sprintf("%s://%s%s", r.URL.Scheme, r.Host, record.URL)
	}

	// Create and execute a new HTTP request based on the stored record.
	client := &http.Client{Timeout: 30 * time.Second}
	httpReq, err := http.NewRequest(record.Method, targetURL, bytes.NewReader(record.Body))
	if err != nil {
		http.Error(w, "Failed to create request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	httpReq.Header = record.Headers.Clone()

	resp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, "Replay failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	replayBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read replay response body: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Forward status code and Content-Type from upstream
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	w.Write(replayBody)
}

// Record request to history
func recordRequest(r *http.Request, body []byte) {
	historyMutex.Lock()
	defer historyMutex.Unlock()
	configLock.RLock()
	historySize := config.HistorySize
	configLock.RUnlock()

	record := RequestRecord{
		ID:        r.Header.Get("X-Request-ID"),
		Timestamp: time.Now(),
		Method:    r.Method,
		URL:       r.RequestURI,
		Headers:   r.Header.Clone(),
		Body:      body,
	}
	requestHistory = append(requestHistory, record)
	if len(requestHistory) > historySize {
		requestHistory = requestHistory[1:]
	}
}
