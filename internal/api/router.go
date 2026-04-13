package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/dev-itbs/lto-reader-api/internal/api/handlers"
	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

// NewRouter creates and configures the chi router with all routes
func NewRouter(r *reader.Reader) *chi.Mux {
	router := chi.NewRouter()

	// Middleware
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.AllowContentType("application/json"))

	// Health check
	router.Get("/health", handlers.HealthHandler)

	// Card operations
	cardHandlers := handlers.NewCardHandlers(r)
	router.Post("/api/v1/card/detect", cardHandlers.Detect)
	router.Post("/api/v1/card/read", cardHandlers.Read)
	router.Post("/api/v1/card/read-all", cardHandlers.ReadAll)
	router.Post("/api/v1/card/read-decoded", cardHandlers.ReadDecoded)
	router.Post("/api/v1/card/decode", cardHandlers.Decode)
	router.Post("/api/v1/card/write", cardHandlers.Write)
	router.Post("/api/v1/card/write-encoded", cardHandlers.WriteEncoded)
	router.Post("/api/v1/card/halt", cardHandlers.Halt)

	// Device operations
	deviceHandlers := handlers.NewDeviceHandlers(r)
	router.Get("/api/v1/device/version", deviceHandlers.GetVersion)
	router.Post("/api/v1/device/beep", deviceHandlers.Beep)
	router.Post("/api/v1/device/reset", deviceHandlers.Reset)
	router.Post("/api/v1/device/eeprom/read", deviceHandlers.ReadEEPROM)
	router.Post("/api/v1/device/eeprom/write", deviceHandlers.WriteEEPROM)
	router.Post("/api/v1/device/value/init", deviceHandlers.InitVal)
	router.Post("/api/v1/device/value/increment", deviceHandlers.Increment)
	router.Post("/api/v1/device/value/decrement", deviceHandlers.Decrement)
	router.Post("/api/v1/device/value/read", deviceHandlers.ReadVal)

	return router
}
