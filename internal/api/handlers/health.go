package handlers

import (
	"encoding/json"
	"net/http"
)

// HealthHandler godoc
//
//	@Summary		Health check
//	@Description	Returns the health status of the API
//	@Tags			Health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/health [get]
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"name":   "D8/T8 Card Reader API",
		"version": "1.0.0",
	})
}
