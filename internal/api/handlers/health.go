package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

// HealthHandler godoc
//
//	@Summary		Health check
//	@Description	Returns the health status of the API and whether the card reader is connected
//	@Tags			Health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/health [get]
func HealthHandler(r *reader.Reader) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		readerStatus := "connected"
		if r == nil {
			readerStatus = "unavailable"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"name":    "D8/T8 Card Reader API",
			"version": "1.0.0",
			"reader":  readerStatus,
		})
	}
}
