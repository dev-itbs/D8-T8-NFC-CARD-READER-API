package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dev-itbs/lto-reader-api/internal/models"
	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

// CardHandlers holds dependencies for card-related handlers
type CardHandlers struct {
	Reader *reader.Reader
}

// NewCardHandlers creates a new CardHandlers instance
func NewCardHandlers(r *reader.Reader) *CardHandlers {
	return &CardHandlers{Reader: r}
}

// Detect detects a card and returns its serial number
func (h *CardHandlers) Detect(w http.ResponseWriter, r *http.Request) {
	var req models.CardDetectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}

	snr, err := h.Reader.DetectCard(req.Mode)
	if err != nil {
		// Parse the error to provide diagnostic info
		diagErr := parseDLLError(err.Error(), "dc_card")
		respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data: models.CardDetectResponse{
			SNRHex:     fmt.Sprintf("0x%08X", snr),
			SNRDecimal: snr,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Read reads a block from a detected and authenticated card
func (h *CardHandlers) Read(w http.ResponseWriter, r *http.Request) {
	var req models.CardReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Block < 0 || req.Block > 63 {
		respondError(w, "Block must be 0-63", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Authenticate the block
	if req.UsePass {
		if err := h.Reader.AuthenticateWithPass(req.KeyMode, req.Block, key); err != nil {
			diagErr := parseDLLError(err.Error(), "dc_authentication_pass")
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.Reader.LoadKey(req.KeyMode, req.Sector, key); err != nil {
			diagErr := parseDLLError(err.Error(), "dc_load_key")
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
		if err := h.Reader.Authenticate(req.KeyMode, req.Sector); err != nil {
			diagErr := parseDLLError(err.Error(), "dc_authentication")
			diagErr.Suggestion = "Verify the key is correct (default: FFFFFFFFFFFF). Try key_mode 0 for KEY A or 4 for KEY B."
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	}

	// Read the block
	data, err := h.Reader.ReadBlock(req.Block)
	if err != nil {
		diagErr := parseDLLError(err.Error(), "dc_read")
		respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
		return
	}

	// Halt the card
	if err := h.Reader.Halt(); err != nil {
		// Log but don't fail the response
		fmt.Printf("Warning: Halt failed: %v\n", err)
	}

	resp := models.Response{
		Success: true,
		Data: models.CardReadResponse{
			Data:      reader.DataToHex(data),
			DataBytes: data[:],
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Write writes a block to a detected and authenticated card
func (h *CardHandlers) Write(w http.ResponseWriter, r *http.Request) {
	var req models.CardWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Block < 0 || req.Block > 63 {
		respondError(w, "Block must be 0-63", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	data, err := reader.DataFromHex(req.Data)
	if err != nil {
		respondError(w, "Invalid data: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Authenticate the block
	if req.UsePass {
		if err := h.Reader.AuthenticateWithPass(req.KeyMode, req.Block, key); err != nil {
			diagErr := parseDLLError(err.Error(), "dc_authentication_pass")
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.Reader.LoadKey(req.KeyMode, req.Sector, key); err != nil {
			diagErr := parseDLLError(err.Error(), "dc_load_key")
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
		if err := h.Reader.Authenticate(req.KeyMode, req.Sector); err != nil {
			diagErr := parseDLLError(err.Error(), "dc_authentication")
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	}

	// Write the block
	if err := h.Reader.WriteBlock(req.Block, data); err != nil {
		diagErr := parseDLLError(err.Error(), "dc_write")
		diagErr.Suggestion = "Ensure block is not read-only (avoid block 3 which is sector trailer). Check write permissions."
		respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
		return
	}

	// Halt the card
	if err := h.Reader.Halt(); err != nil {
		// Log but don't fail the response
		fmt.Printf("Warning: Halt failed: %v\n", err)
	}

	resp := models.Response{
		Success: true,
		Data:    models.CardWriteResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Halt halts the card
func (h *CardHandlers) Halt(w http.ResponseWriter, r *http.Request) {
	if err := h.Reader.Halt(); err != nil {
		respondError(w, "Halt failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    map[string]string{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// respondError is a helper to send error responses
func respondError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.Response{
		Success: false,
		Message: message,
		Code:    statusCode,
	})
}

// respondWithError sends a detailed error response with diagnostic info
func respondWithError(w http.ResponseWriter, title string, message string, statusCode int, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    false,
		"error":      title,
		"message":    message,
		"code":       statusCode,
		"retryable":  retryable,
	})
}

// parseDLLError extracts error code from DLL error string
func parseDLLError(errStr string, function string) *reader.DiagnosticError {
	// Try to extract error code from error string
	// Format: "error message: dc_function returned X"
	var code int
	fmt.Sscanf(errStr, "dc_%*s returned %d", &code)
	if code == 0 {
		// Fallback: try to find any integer in the error
		fmt.Sscanf(errStr, "%*s %d %*s", &code)
	}
	return reader.NewDiagnosticError(code, function, "")
}

// respondWithDiagnosticError sends a detailed diagnostic error response
func respondWithDiagnosticError(w http.ResponseWriter, diagErr *reader.DiagnosticError, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         false,
		"error":           diagErr.Message,
		"error_code":      diagErr.Code,
		"function":        diagErr.Function,
		"suggestion":      diagErr.Suggestion,
		"details":         diagErr.Details,
		"card_type":       diagErr.CardType,
		"retryable":       diagErr.Retryable,
		"documentation":   diagErr.Documentation,
		"code":            statusCode,
	})
}
