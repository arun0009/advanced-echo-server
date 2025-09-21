package main

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"time"
)

// Health check handlers
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now(),
		"uptime":    time.Since(startTime).String(),
	})
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// Server info handler
func infoHandler(w http.ResponseWriter, r *http.Request) {
	configLock.RLock()
	maxBodySize := config.MaxBodySize
	hostname := config.Hostname
	configLock.RUnlock()

	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	} else {
		body = []byte{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"timestamp":    time.Now(),
		"method":       r.Method,
		"url":          r.RequestURI,
		"path":         r.URL.Path,
		"query":        r.URL.Query(),
		"headers":      r.Header,
		"body_size":    len(body),
		"remote_addr":  getClientIP(r),
		"user_agent":   r.UserAgent(),
		"content_type": r.Header.Get("Content-Type"),
		"protocol":     r.Proto,
		"tls":          r.TLS != nil,
		"request_id":   r.Header.Get("X-Request-ID"),
		"server": map[string]interface{}{
			"hostname":      hostname,
			"version":       "1.0.0",
			"go_version":    runtime.Version(),
			"platform":      runtime.GOOS + "/" + runtime.GOARCH,
			"start_time":    startTime,
			"uptime":        time.Since(startTime).String(),
			"request_count": requestCounter,
		},
	})
}
