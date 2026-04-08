package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	log.Printf("Starting D8/T8 Card Reader API")
	log.Printf("Reader Port: %d, Baud: %d", cfg.ReaderPort, cfg.ReaderBaud)
	log.Printf("Server Address: %s", cfg.ServerAddr)
	log.Printf("DLL Name: %s", cfg.DLLName)

	// Initialize the card reader
	r, err := reader.New(cfg.DLLName, cfg.ReaderPort, cfg.ReaderBaud)
	if err != nil {
		log.Fatalf("Failed to initialize reader: %v", err)
	}
	defer r.Close()

	log.Println("Card reader initialized successfully")

	// Create router
	router := api.NewRouter(r)

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
