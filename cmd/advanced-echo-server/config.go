package main

import (
	"os"
	"strconv"
)

// Config holds server configuration
type Config struct {
	Port               string
	EnableTLS          bool
	CertFile           string
	KeyFile            string
	EnableCORS         bool
	LogRequests        bool
	LogHeaders         bool
	LogBody            bool
	LogTransaction     bool
	LogResponse        bool
	LogResponseHeaders bool
	LogResponseBody    bool
	MaxBodySize        int64
	MaxLogBodySize     int64
	Hostname           string
	HistorySize        int
	ScenarioFile       string
	RateLimitRPS       float64
	RateLimitBurst     int
}

// Scenario defines a sequence of responses for an endpoint
type Scenario struct {
	Path      string     `yaml:"path" json:"path"`
	Responses []Response `yaml:"responses" json:"responses"`
}

// Response defines a single response in a scenario
type Response struct {
	Status int    `yaml:"status" json:"status"`
	Delay  string `yaml:"delay" json:"delay"`
	Body   string `yaml:"body" json:"body"`
}

// loadConfigFromEnv builds a Config from environment variables.
func loadConfigFromEnv() Config {
	return Config{
		Port:               getEnv("PORT", "8080"),
		EnableTLS:          getEnv("ENABLE_TLS", "false") == "true",
		CertFile:           getEnv("CERT_FILE", "server.crt"),
		KeyFile:            getEnv("KEY_FILE", "server.key"),
		EnableCORS:         getEnv("ENABLE_CORS", "true") == "true",
		LogRequests:        getEnv("LOG_REQUESTS", "true") == "true",
		LogHeaders:         getEnv("LOG_HEADERS", "false") == "true",
		LogBody:            getEnv("LOG_BODY", "false") == "true",
		LogTransaction:     getEnv("LOG_TRANSACTION", "false") == "true",
		LogResponse:        getEnv("LOG_RESPONSE", "false") == "true",
		LogResponseHeaders: getEnv("LOG_RESPONSE_HEADERS", "false") == "true",
		LogResponseBody:    getEnv("LOG_RESPONSE_BODY", "false") == "true",
		MaxBodySize:        parseInt64(getEnv("MAX_BODY_SIZE", "10485760")),
		MaxLogBodySize:     parseInt64(getEnv("MAX_LOG_BODY_SIZE", "2048")),
		HistorySize:        int(parseInt64(getEnv("ECHO_HISTORY_SIZE", "100"))),
		ScenarioFile:       getEnv("ECHO_SCENARIO_FILE", "scenarios.yaml"),
		RateLimitRPS:       parseFloat64(getEnv("ECHO_RATE_LIMIT_RPS", "0")),
		RateLimitBurst:     int(parseInt64(getEnv("ECHO_RATE_LIMIT_BURST", "0"))),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseInt64(s string) int64 {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	return 0
}

func parseFloat64(s string) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return 0
}
