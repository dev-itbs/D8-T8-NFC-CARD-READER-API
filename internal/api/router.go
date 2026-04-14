package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/dev-itbs/lto-reader-api/internal/api/handlers"
	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

// NewRouter creates and configures the chi router with all routes
func NewRouter(r *reader.Reader, salt string) *chi.Mux {
	router := chi.NewRouter()

	// Middleware
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.AllowContentType("application/json"))

	// Health check
	router.Get("/health", handlers.HealthHandler(r))

	// Swagger docs
	router.Get("/api/docs", docsHandler)
	router.Get("/api/docs/openapi.json", openapiHandler)

	// Card Operations
	cardHandlers := handlers.NewCardHandlers(r, salt)
	router.Post("/api/v1/card/detect", cardHandlers.Detect)
	router.Post("/api/v1/card/identify", cardHandlers.Identify)
	router.Post("/api/v1/card/halt", cardHandlers.Halt)
	router.Post("/api/v1/card/set-password", cardHandlers.SetPassword)
	router.Post("/api/v1/card/remove-password", cardHandlers.RemovePassword)

	// MD5 Endpoints
	router.Post("/api/v1/card/1/read-decoded", cardHandlers.ReadDecodedMD5)
	router.Post("/api/v1/card/1/write-encoded", cardHandlers.WriteEncodedMD5)

	// SHA256 Endpoints
	router.Post("/api/v1/card/2/read-decoded", cardHandlers.ReadDecodedSHA)
	router.Post("/api/v1/card/2/write-encoded", cardHandlers.WriteEncodedSHA)

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
