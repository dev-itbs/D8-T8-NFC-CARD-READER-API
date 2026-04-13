// @title LTO NFC Card Reader API
// @version 1.0.0
// @description REST API for reading and writing Branca-encoded data on Mifare Classic NFC cards using D8/T8 hardware.
// @host localhost:8080
// @BasePath /
//
// @tag.name Health
// @tag.description API liveness check
//
// @tag.name Card Operations
// @tag.description Basic card detect and halt operations
//
// @tag.name MD5 Endpoints
// @tag.description Branca token read/write with MD5-derived keys (endpoint group 1)
//
// @tag.name SHA256 Endpoints
// @tag.description Branca token read/write with SHA256+salt-derived keys (endpoint group 2)
//
// @tag.name Device
// @tag.description Device control, EEPROM access, and value block operations
package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dev-itbs/lto-reader-api/config"
	"github.com/dev-itbs/lto-reader-api/internal/api"
	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Setup logging to file
	if err := setupLogging(cfg.LogFile); err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}

	log.Printf("Starting D8/T8 Card Reader API")
	log.Printf("Reader Port: %d, Baud: %d", cfg.ReaderPort, cfg.ReaderBaud)
	log.Printf("Server Address: %s", cfg.ServerAddr)
	log.Printf("DLL Path: %s", cfg.DLLPath)
	log.Printf("Log File: %s", cfg.LogFile)

	// Initialize the card reader
	r, err := reader.New(cfg.DLLPath, cfg.ReaderPort, cfg.ReaderBaud)
	if err != nil {
		log.Fatalf("Failed to initialize reader: %v", err)
	}
	defer r.Close()

	log.Println("Card reader initialized successfully")

	// Create router
	router := api.NewRouter(r, cfg.BrancaSalt)

	// Create HTTP server
	server := &http.Server{
		Addr:         cfg.ServerAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Server starting on %s", cfg.ServerAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutdown signal received, gracefully stopping server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.GracefulWait)*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}

// setupLogging configures logging to both console and file
func setupLogging(logFile string) error {
	// Create logs directory if it doesn't exist
	logDir := filepath.Dir(logFile)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	// Open log file for writing (append mode)
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	// Write to both console and file
	multiWriter := io.MultiWriter(os.Stdout, file)
	log.SetOutput(multiWriter)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	return nil
}
