package main

import (
	"net/http"
)

// rateLimitMiddleware enforces a global rate limiter, skipping SSE path.
func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for SSE
		if r.URL.Path == "/sse" {
			next.ServeHTTP(w, r)
			return
		}
		if !rateLimiter.Allow() {
			// Set headers before writing status/body
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			chaosErrors.WithLabelValues("rate_limit").Inc()
			return
		}
		next.ServeHTTP(w, r)
	})
}
