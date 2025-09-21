package main

import (
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// setupRoutes configures all HTTP routes and middleware for the server.
func setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Apply middleware
	router.Use(loggingMiddleware)
	router.Use(corsMiddleware)
	router.Use(requestIDMiddleware)
	if rateLimiter != nil {
		router.Use(rateLimitMiddleware)
	}

	// Health check endpoints
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/ready", readyHandler).Methods("GET")

	// Server info
	router.HandleFunc("/info", infoHandler).Methods("GET")

	// WebSocket and SSE endpoints
	// router.HandleFunc("/ws", websocketHandler) // WebSocket route (if needed)
	router.HandleFunc("/sse", sseHandler)

	// Embedded frontend for SSE
	router.HandleFunc("/web-sse", serveFrontendSSE)

	// Request history and replay
	router.HandleFunc("/history", historyHandler).Methods("GET")
	router.HandleFunc("/replay", replayHandler).Methods("POST")

	// Scenario management
	router.HandleFunc("/scenario", scenarioHandler).Methods("GET", "POST")

	// Prometheus metrics
	router.Handle("/metrics", promhttp.Handler())

	// Everything else is pure echo with testing features
	router.PathPrefix("/").HandlerFunc(echoHandler)

	return router
}
