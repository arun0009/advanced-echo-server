package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// loggingMiddleware logs requests and records Prometheus metrics.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Protect access to config fields
		configLock.RLock()
		logRequests := config.LogRequests
		logHeaders := config.LogHeaders
		logBody := config.LogBody
		logResp := config.LogResponse
		logRespHeaders := config.LogResponseHeaders
		logRespBody := config.LogResponseBody
		maxLogBody := config.MaxLogBodySize
		logTxn := config.LogTransaction
		configLock.RUnlock()

		// Basic request line
		if logRequests {
			log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		}

		// Optional: headers
		if logHeaders {
			log.Printf("Headers: %+v", r.Header)
		}

		// Request body capture (skip SSE). Capture if legacy flag or transaction logging is enabled.
		var reqBody []byte
		if (logBody || logTxn) && r.URL.Path != "/sse" {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("Error reading body: %v", err)
			} else {
				reqBody = b
				trunc := reqBody
				if maxLogBody > 0 && int64(len(trunc)) > maxLogBody {
					trunc = trunc[:maxLogBody]
				}
				if logBody {
					log.Printf("Body: %s", string(trunc))
				}
				r.Body = io.NopCloser(bytes.NewReader(reqBody))
			}
		}

		// Wrap response writer to capture status and body
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		// Completion line
		if logRequests {
			log.Printf("%s %s %s - %d %v", r.RemoteAddr, r.Method, r.URL.Path, rw.statusCode, time.Since(start))
		}

		// Optional: response logging
		if logResp {
			if logRespHeaders {
				log.Printf("Response headers: %+v", rw.Header())
			}
			if logRespBody && r.URL.Path != "/sse" {
				respBody := rw.bodyBuf.Bytes()
				trunc := respBody
				if maxLogBody > 0 && int64(len(trunc)) > maxLogBody {
					trunc = trunc[:maxLogBody]
				}
				log.Printf("Response body: %s", string(trunc))
			}
		}

		// Consolidated transaction block (plain text)
		if logTxn {
			log.Printf("--- transaction start ---")
			log.Printf("REQUEST: %s %s", r.Method, r.URL.Path)
			log.Printf("Headers: %+v", r.Header)
			if r.URL.Path != "/sse" {
				tr := reqBody
				if maxLogBody > 0 && int64(len(tr)) > maxLogBody {
					tr = tr[:maxLogBody]
				}
				log.Printf("Body: %s", string(tr))
			} else {
				log.Printf("Body: [omitted for /sse]")
			}

			log.Printf("RESPONSE: %d", rw.statusCode)
			log.Printf("Headers: %+v", rw.Header())
			if r.URL.Path != "/sse" {
				respBody := rw.bodyBuf.Bytes()
				tr2 := respBody
				if maxLogBody > 0 && int64(len(tr2)) > maxLogBody {
					tr2 = tr2[:maxLogBody]
				}
				log.Printf("Body: %s", string(tr2))
			} else {
				log.Printf("Body: [omitted for /sse]")
			}
			log.Printf("Duration: %v", time.Since(start))
			log.Printf("--- transaction end ---")
		}

		// Record latency for Prometheus
		requestLatency.Observe(time.Since(start).Seconds())
		requestTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(rw.statusCode)).Inc()
	})
}
