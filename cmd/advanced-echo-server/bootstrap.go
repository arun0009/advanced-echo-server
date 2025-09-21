package main

import (
	"log"
	"os"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

var (
	configLock sync.RWMutex
	config     Config
	startTime  time.Time
)

// initializeServer performs explicit initialization previously done in init().
func initializeServer() {
	startTime = time.Now()

	// Load configuration from environment
	cfg := loadConfigFromEnv()
	configLock.Lock()
	config = cfg
	if hostname, _ := os.Hostname(); hostname != "" {
		config.Hostname = hostname
	}
	configLock.Unlock()

	// Initialize request history
	configLock.RLock()
	if config.HistorySize > 0 {
		requestHistory = make([]RequestRecord, 0, config.HistorySize)
	}
	configLock.RUnlock()

	// Initialize rate limiter
	configLock.RLock()
	if config.RateLimitRPS > 0 && config.RateLimitBurst > 0 {
		rateLimiter = rate.NewLimiter(rate.Limit(config.RateLimitRPS), config.RateLimitBurst)
	}
	configLock.RUnlock()

	// Load scenarios from YAML if specified
	configLock.RLock()
	if config.ScenarioFile != "" {
		if data, err := os.ReadFile(config.ScenarioFile); err == nil {
			var sc []Scenario
			if err := yaml.Unmarshal(data, &sc); err == nil {
				for _, s := range sc {
					scenarios.Store(s.Path, s.Responses)
					scenarioIndex.Store(s.Path, 0)
				}
			} else {
				log.Printf("Failed to parse scenario file: %v", err)
			}
		}
	}
	configLock.RUnlock()

	// Register Prometheus metrics
	registerPrometheusMetrics()
}
