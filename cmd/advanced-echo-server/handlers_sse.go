package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

// Server-Sent Events handler
func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("Streaming not supported for %s", r.RemoteAddr)
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	log.Printf("SSE connection established for %s", r.RemoteAddr)

	tickerIntervalStr := getEnv("ECHO_SSE_TICKER", "5s")
	tickerInterval, err := time.ParseDuration(tickerIntervalStr)
	if err != nil || tickerInterval <= 0 {
		log.Printf("Invalid ECHO_SSE_TICKER '%s', using default 5s: %v", tickerIntervalStr, err)
		tickerInterval = 5 * time.Second
	}
	ticker := time.NewTicker(tickerInterval)
	keepAlive := time.NewTicker(1 * time.Second) // Send keep-alive every second
	defer ticker.Stop()
	defer keepAlive.Stop()

	counter := 0
	for {
		select {
		case <-r.Context().Done():
			log.Printf("SSE connection closed by client %s: %v", r.RemoteAddr, r.Context().Err())
			return
		case <-ticker.C:
			counter++
			data := map[string]interface{}{
				"counter":   counter,
				"timestamp": time.Now(),
				"uptime":    time.Since(startTime).String(),
			}
			jsonData, err := json.Marshal(data)
			if err != nil {
				log.Printf("SSE JSON marshal error for %s: %v", r.RemoteAddr, err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			_, err = fmt.Fprintf(w, "data: %s\n\n", jsonData)
			if err != nil {
				log.Printf("SSE write error for %s: %v", r.RemoteAddr, err)
				return
			}
			log.Printf("SSE sent event %d to %s", counter, r.RemoteAddr)
			flusher.Flush()
		case <-keepAlive.C:
			_, err := fmt.Fprintf(w, ": keep-alive\n\n")
			if err != nil {
				log.Printf("SSE keep-alive write error for %s: %v", r.RemoteAddr, err)
				return
			}
			flusher.Flush()
		}
	}
}

// Frontend for SSE
func serveFrontendSSE(w http.ResponseWriter, r *http.Request) {
	const templateName = "html/sse.html"
	tmpl, err := template.ParseFS(files, templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templateData := struct {
		Host string
	}{
		Host: r.Host,
	}
	err = tmpl.Execute(w, templateData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
}
