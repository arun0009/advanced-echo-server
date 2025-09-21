package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
)

// responseWriter wraps http.ResponseWriter to capture status code and body and support Flush/Hijack.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
	bodyBuf    bytes.Buffer // captures response body up to MaxLogBodySize in middleware
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.ResponseWriter.WriteHeader(code)
		rw.written = true
	}
}

func (rw *responseWriter) Write(p []byte) (int, error) {
	// Always write to the underlying writer
	n, err := rw.ResponseWriter.Write(p)
	// Capture up to configured limit; limit is enforced in middleware where it's read
	// Here we just buffer everything; middleware will truncate on read
	rw.bodyBuf.Write(p[:n])
	return n, err
}

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("response does not implement http.Hijacker")
}
