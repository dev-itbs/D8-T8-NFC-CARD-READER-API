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
	router.Get("/health", handlers.HealthHandler)

	// Card operations
	cardHandlers := handlers.NewCardHandlers(r, salt)
	router.Post("/api/v1/card/detect", cardHandlers.Detect)
	router.Post("/api/v1/card/read", cardHandlers.Read)
	router.Post("/api/v1/card/read-all", cardHandlers.ReadAll)
	// MD5-keyed Branca endpoints (key = hex(MD5(snr_decimal)))
	router.Post("/api/v1/card/read-decoded-md5", cardHandlers.ReadDecodedMD5)
	router.Post("/api/v1/card/decode-md5", cardHandlers.DecodeMD5)
	router.Post("/api/v1/card/write-encoded-md5", cardHandlers.WriteEncodedMD5)
	// SHA256+salt-keyed Branca endpoints (key = SHA256(snr_decimal + BRANCA_SALT))
	router.Post("/api/v1/card/read-decoded-sha", cardHandlers.ReadDecodedSHA)
	router.Post("/api/v1/card/decode-sha", cardHandlers.DecodeSHA)
	router.Post("/api/v1/card/write-encoded-sha", cardHandlers.WriteEncodedSHA)
	router.Post("/api/v1/card/write", cardHandlers.Write)
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
