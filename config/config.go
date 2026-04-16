package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration
type Config struct {
	ReaderPort   int
	ReaderBaud   int
	ServerAddr   string
	DLLPath      string
	LogFile      string
	GracefulWait int    // seconds
	BrancaSalt   string // salt for SHA-based Branca key derivation
}

// Load reads configuration from environment variables with sensible defaults
func Load() *Config {
	cfg := &Config{
		ReaderPort:   getEnvInt("READER_PORT", 100),         // 100 = USB
		ReaderBaud:   getEnvInt("READER_BAUD", 115200),      // baud rate
		ServerAddr:   getEnv("SERVER_ADDR", ":8080"),        // HTTP server bind address
		DLLPath:      getEnv("DLL_PATH", "dc_sdk.dll"),      // DLL file path
		LogFile:      getEnv("LOG_FILE", "logs/reader.log"), // Log file path
		GracefulWait: getEnvInt("GRACEFUL_WAIT", 5),         // shutdown grace period in seconds
		BrancaSalt:   getEnv("BRANCA_SALT", ""),             // salt for SHA Branca key
	}
	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
		fmt.Fprintf(os.Stderr, "warning: invalid integer for %s, using default %d\n", key, defaultValue)
	}
	return defaultValue
}

// Validate checks the configuration for validity
func (c *Config) Validate() error {
	if c.ReaderPort < 0 || (c.ReaderPort > 3 && c.ReaderPort != 100) {
		return fmt.Errorf("invalid READER_PORT: must be 0-3 or 100 (USB)")
	}
	if c.ReaderBaud < 9600 || c.ReaderBaud > 115200 {
		return fmt.Errorf("invalid READER_BAUD: must be 9600-115200")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("SERVER_ADDR cannot be empty")
	}
	if c.DLLPath == "" {
		return fmt.Errorf("DLL_PATH cannot be empty")
	}
	if c.LogFile == "" {
		return fmt.Errorf("LOG_FILE cannot be empty")
	}
	return nil
}
