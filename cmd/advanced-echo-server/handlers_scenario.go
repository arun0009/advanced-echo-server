package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Scenario handler
func scenarioHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		var result []Scenario
		scenarios.Range(func(key, value interface{}) bool {
			path := key.(string)
			responses := value.([]Response)
			result = append(result, Scenario{Path: path, Responses: responses})
			return true
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	var scenariosData []Scenario
	if err := json.NewDecoder(r.Body).Decode(&scenariosData); err != nil {
		http.Error(w, "Invalid scenario data", http.StatusBadRequest)
		return
	}
	for _, s := range scenariosData {
		scenarios.Store(s.Path, s.Responses)
		scenarioIndex.Store(s.Path, 0)
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "scenarios updated"})
}

// Process scenario responses
func processScenario(w http.ResponseWriter, r *http.Request) bool {
	scenario, ok := scenarios.Load(r.URL.Path)
	if !ok {
		return false
	}
	responses := scenario.([]Response)
	idx, _ := scenarioIndex.LoadOrStore(r.URL.Path, 0)
	index := idx.(int) % len(responses)
	resp := responses[index]
	scenarioIndex.Store(r.URL.Path, index+1)

	// Apply delay from scenario
	if resp.Delay != "" {
		if strings.Contains(resp.Delay, "-") {
			parts := strings.Split(resp.Delay, "-")
			if len(parts) == 2 {
				minStr := strings.TrimSuffix(parts[0], "ms")
				maxStr := strings.TrimSuffix(parts[1], "ms")
				min, err1 := strconv.Atoi(minStr)
				max, err2 := strconv.Atoi(maxStr)
				if err1 == nil && err2 == nil && max >= min {
					delay := min + rng.Intn(max-min+1)
					log.Printf("Scenario delay: %dms (range: %d-%d)", delay, min, max)
					time.Sleep(time.Duration(delay) * time.Millisecond)
				}
			}
		} else {
			delayStr := strings.TrimSuffix(resp.Delay, "ms")
			if ms, err := strconv.Atoi(delayStr); err == nil {
				if ms > 300000 {
					ms = 300000
				}
				log.Printf("Scenario delay: %dms", ms)
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
		}
	}

	w.Header().Set("X-Echo-Scenario", "true")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	if resp.Body != "" {
		w.Write([]byte(resp.Body))
	} else {
		w.Write([]byte(echoRequestInfo(r)))
	}
	return true
}
