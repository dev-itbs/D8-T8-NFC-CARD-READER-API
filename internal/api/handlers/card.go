package handlers

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

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
	log.Println("[API] POST /api/v1/card/detect")
	var req models.CardDetectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[API] ERROR: Invalid JSON: %v", err)
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}

	log.Printf("[API] Detect request: mode=%d", req.Mode)
	snr, err := h.Reader.DetectCard(req.Mode)
	if err != nil {
		log.Printf("[API] ERROR: DetectCard failed: %v", err)
		// Parse the error to provide diagnostic info
		diagErr := parseDLLError(err.Error(), "dc_request")

		// If no card detected, return 404 not 500
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no card detected") || strings.Contains(err.Error(), "no card present") {
			statusCode = http.StatusNotFound
			diagErr.Suggestion = "No card detected. Ensure card is placed on the reader and in range."
		}

		respondWithDiagnosticError(w, diagErr, statusCode)
		return
	}

	log.Printf("[API] Card detected: SNR=%08X", snr)
	resp := models.Response{
		Success: true,
		Message: "Card detected and selected. Next: authenticate with /card/read or /card/write",
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
	log.Println("[API] POST /api/v1/card/read")
	var req models.CardReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[API] ERROR: Invalid JSON: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Block < 0 || req.Block > 63 {
		log.Printf("[API] ERROR: Invalid block: %d", req.Block)
		respondError(w, "Block must be 0-63", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Auto-detect card if not already selected (previous operation may have halted it)
	log.Printf("[API] Auto-detecting card (mode=%d)...", req.Mode)
	snr, err := h.Reader.DetectCard(req.Mode)
	if err != nil {
		log.Printf("[API] ERROR: Auto-detect failed: %v", err)
		diagErr := parseDLLError(err.Error(), "dc_request")
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no card detected") || strings.Contains(err.Error(), "no card present") {
			statusCode = http.StatusNotFound
			diagErr.Suggestion = "No card detected. Place card on reader before reading."
		}
		respondWithDiagnosticError(w, diagErr, statusCode)
		return
	}
	log.Printf("[API] Card auto-detected: SNR=%08X", snr)

	// Authenticate the block
	if req.UsePass {
		log.Printf("[API] Using direct pass auth: mode=%d, block=%d", req.KeyMode, req.Block)
		if err := h.Reader.AuthenticateWithPass(req.KeyMode, req.Block, key); err != nil {
			log.Printf("[API] AuthenticateWithPass failed: %v", err)
			diagErr := parseDLLError(err.Error(), "dc_authentication_pass")
			diagErr.Suggestion = "Direct auth failed. Ensure key is correct and block exists."
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	} else {
		log.Printf("[API] Using load_key+auth workflow: sector=%d, mode=%d, key=%s", req.Sector, req.KeyMode, req.Key)
		if err := h.Reader.LoadKey(req.KeyMode, req.Sector, key); err != nil {
			log.Printf("[API] LoadKey failed: %v", err)
			diagErr := parseDLLError(err.Error(), "dc_load_key")
			diagErr.Suggestion = fmt.Sprintf("LoadKey failed for sector %d. Key may be incorrect.", req.Sector)
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
		if err := h.Reader.Authenticate(req.KeyMode, req.Sector); err != nil {
			log.Printf("[API] Authenticate failed: %v", err)
			diagErr := parseDLLError(err.Error(), "dc_authentication")
			diagErr.Suggestion = fmt.Sprintf("Auth failed for sector %d with key_mode %d. Try: (1) key_mode 0 for KEY A or 4 for KEY B, (2) verify key is correct (default: FFFFFFFFFFFF for factory blank cards), (3) ensure sector exists (0-15).", req.Sector, req.KeyMode)
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
		Message: fmt.Sprintf("Block %d read successfully. Workflow: DETECT → READ/WRITE → [REPEAT]. Next: write data, read next block, or run another operation.", req.Block),
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
	log.Println("[API] POST /api/v1/card/write")
	var req models.CardWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[API] ERROR: Invalid JSON: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Block < 0 || req.Block > 63 {
		log.Printf("[API] ERROR: Invalid block: %d", req.Block)
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

	// Auto-detect card if not already selected (previous operation may have halted it)
	log.Printf("[API] Auto-detecting card (mode=%d)...", req.Mode)
	snr, err := h.Reader.DetectCard(req.Mode)
	if err != nil {
		log.Printf("[API] ERROR: Auto-detect failed: %v", err)
		diagErr := parseDLLError(err.Error(), "dc_request")
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no card detected") || strings.Contains(err.Error(), "no card present") {
			statusCode = http.StatusNotFound
			diagErr.Suggestion = "No card detected. Place card on reader before writing."
		}
		respondWithDiagnosticError(w, diagErr, statusCode)
		return
	}
	log.Printf("[API] Card auto-detected: SNR=%08X", snr)

	// Authenticate the block
	if req.UsePass {
		log.Printf("[API] Using direct pass auth: mode=%d, block=%d", req.KeyMode, req.Block)
		if err := h.Reader.AuthenticateWithPass(req.KeyMode, req.Block, key); err != nil {
			log.Printf("[API] AuthenticateWithPass failed: %v", err)
			diagErr := parseDLLError(err.Error(), "dc_authentication_pass")
			diagErr.Suggestion = "Direct auth failed. Ensure key is correct and block exists."
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	} else {
		log.Printf("[API] Using load_key+auth workflow: sector=%d, mode=%d, key=%s", req.Sector, req.KeyMode, req.Key)
		if err := h.Reader.LoadKey(req.KeyMode, req.Sector, key); err != nil {
			log.Printf("[API] LoadKey failed: %v", err)
			diagErr := parseDLLError(err.Error(), "dc_load_key")
			diagErr.Suggestion = fmt.Sprintf("LoadKey failed for sector %d. Key may be incorrect.", req.Sector)
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
		if err := h.Reader.Authenticate(req.KeyMode, req.Sector); err != nil {
			log.Printf("[API] Authenticate failed: %v", err)
			diagErr := parseDLLError(err.Error(), "dc_authentication")
			diagErr.Suggestion = fmt.Sprintf(
				"WORKFLOW: DETECT → AUTH → WRITE. Auth failed for sector %d (key_mode=%d).\n"+
					"Fixes: (1) Verify key is correct (default: FFFFFFFFFFFF); (2) Try key_mode 4 instead of 0 (KEY B vs KEY A); (3) Ensure sector exists (0-15).",
				req.Sector, req.KeyMode)
			respondWithDiagnosticError(w, diagErr, http.StatusInternalServerError)
			return
		}
	}

	// Write the block
	log.Printf("[API] Writing block %d", req.Block)
	if err := h.Reader.WriteBlock(req.Block, data); err != nil {
		log.Printf("[API] WriteBlock failed: %v", err)
		diagErr := parseDLLError(err.Error(), "dc_write")
		blockInSector := req.Block % 4
		diagErr.Suggestion = fmt.Sprintf(
			"WORKFLOW: DETECT → AUTH → WRITE.\nWrite failed for block %d (block_in_sector=%d). Block 3 in each sector is sector trailer (read-only). Try blocks 0-2.",
			req.Block, blockInSector)
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
		Message: fmt.Sprintf("Block %d written successfully. Workflow: DETECT → AUTH → WRITE. Next: read to verify, write another block, or repeat.", req.Block),
		Data:    models.CardWriteResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// ReadAll reads all 64 blocks from the card and concatenates them
func (h *CardHandlers) ReadAll(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/read-all")
	var req models.CardReadAllRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[API] ERROR: Invalid JSON: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		log.Printf("[API] ERROR: Invalid key: %v", err)
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Auto-detect card
	log.Printf("[API] Auto-detecting card (mode=%d)...", req.Mode)
	snr, err := h.Reader.DetectCard(req.Mode)
	if err != nil {
		log.Printf("[API] ERROR: Auto-detect failed: %v", err)
		diagErr := parseDLLError(err.Error(), "dc_request")
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no card detected") || strings.Contains(err.Error(), "no card present") {
			statusCode = http.StatusNotFound
			diagErr.Suggestion = "WORKFLOW: DETECT → READ-ALL.\nNo card detected. Place card on reader before reading all blocks."
		}
		respondWithDiagnosticError(w, diagErr, statusCode)
		return
	}
	log.Printf("[API] Card auto-detected: SNR=%08X", snr)

	// Read all 64 blocks
	log.Printf("[API] Reading all 64 blocks...")
	fullDataHex := ""
	blockMap := make(map[string]string)
	failedBlocks := []int{}

	for block := 0; block < 64; block++ {
		// Re-authenticate for each sector (every 4 blocks = 1 sector)
		if block%4 == 0 {
			sector := block / 4
			log.Printf("[API] Authenticating sector %d...", sector)

			if req.UsePass {
				if err := h.Reader.AuthenticateWithPass(req.KeyMode, block, key); err != nil {
					log.Printf("[API] AuthenticateWithPass failed for block %d: %v", block, err)
					failedBlocks = append(failedBlocks, block)
					continue
				}
			} else {
				if err := h.Reader.LoadKey(req.KeyMode, sector, key); err != nil {
					log.Printf("[API] LoadKey failed for sector %d: %v", sector, err)
					failedBlocks = append(failedBlocks, block)
					continue
				}
				if err := h.Reader.Authenticate(req.KeyMode, sector); err != nil {
					log.Printf("[API] Authenticate failed for sector %d: %v", sector, err)
					failedBlocks = append(failedBlocks, block)
					continue
				}
			}
		}

		// Read block
		data, err := h.Reader.ReadBlock(block)
		if err != nil {
			log.Printf("[API] ReadBlock failed for block %d: %v", block, err)
			failedBlocks = append(failedBlocks, block)
			blockMap[fmt.Sprintf("%d", block)] = "ERROR"
			continue
		}

		hexData := reader.DataToHex(data)
		blockMap[fmt.Sprintf("%d", block)] = hexData
		fullDataHex += hexData

		if block%16 == 15 {
			log.Printf("[API] Read blocks %d-%d", block-15, block)
		}
	}

	// Halt the card
	if err := h.Reader.Halt(); err != nil {
		log.Printf("Warning: Halt failed: %v", err)
	}

	// Convert to base64
	fullDataBytes, _ := hex.DecodeString(fullDataHex)
	fullDataBase64 := base64.StdEncoding.EncodeToString(fullDataBytes)

	log.Printf("[API] ReadAll complete: %d blocks read, %d blocks failed", 64-len(failedBlocks), len(failedBlocks))

	resp := models.Response{
		Success: len(failedBlocks) == 0,
		Message: fmt.Sprintf("Read all blocks. Total: %d bytes. Failed blocks: %d", len(fullDataBytes), len(failedBlocks)),
		Data: models.CardReadAllResponse{
			SNRHex:         fmt.Sprintf("0x%08X", snr),
			SNRDecimal:     snr,
			TotalBytes:     len(fullDataBytes),
			FullData:       fullDataHex,
			FullDataBase64: fullDataBase64,
			BlockMap:       blockMap,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Halt halts the card
func (h *CardHandlers) Halt(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/halt")
	if err := h.Reader.Halt(); err != nil {
		log.Printf("[API] ERROR: Halt failed: %v", err)
		respondError(w, "Halt failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Message: "Card halted (deselected). Workflow: DETECT → [READ/WRITE] → HALT → [REPEAT]. Next: call /card/detect to select another card or perform more operations.",
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
// Format examples:
// "dc_request returned 1 (no card detected)"
// "dc_authentication returned 255"
func parseDLLError(errStr string, function string) *reader.DiagnosticError {
	var code int
	// Try pattern: "dc_XXX returned NNN ..."
	n, _ := fmt.Sscanf(errStr, "dc_%*s returned %d", &code)
	if n <= 0 {
		// Fallback: just find first integer
		fmt.Sscanf(errStr, "%*s %d", &code)
	}

	diagErr := reader.NewDiagnosticError(code, function, "")
	return diagErr
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
