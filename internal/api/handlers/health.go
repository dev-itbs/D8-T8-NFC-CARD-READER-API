package handlers

import (
	"encoding/json"
	"net/http"
)

// HealthHandler returns the health status of the API
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"name":   "D8/T8 Card Reader API",
		"version": "1.0.0",
	})
}
