package handlers

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/hako/branca"

	"github.com/dev-itbs/lto-reader-api/internal/models"
	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

// snrToBrancaKey derives a 32-byte Branca key from a card SNR decimal:
// key = hex(MD5(string(snr_decimal)))
func snrToBrancaKey(snr uint32) string {
	snrStr := strconv.FormatUint(uint64(snr), 10)
	sum := md5.Sum([]byte(snrStr))
	return hex.EncodeToString(sum[:]) // 32 hex chars = 32 bytes
}

// extractNDEFText parses raw Mifare block data and extracts the payload of the
// first NDEF text record ('T'). It handles the NDEF TLV wrapper (0x03 marker)
// and strips the language-code prefix from the text payload, returning the
// bare Branca token string.
//
// Layout on a Mifare Classic 1K NDEF card written by Android NFC Tools:
//   sector 0  (blocks 0-3)  – UID/MFG + MAD (contains false 0x03 bytes)
//   sector 1+ (blocks 4-63) – CC (E1 10 ...) followed by NDEF TLV: 03 [len] [records]
func extractNDEFText(raw []byte) string {
	// Start from byte 64 (skip sector 0: UID/MFG block + MAD data).
	// Sector 0 MAD contains 0x03 bytes that look like NDEF TLV markers but aren't.
	const startOffset = 64
	if len(raw) < startOffset+3 {
		return ""
	}

	for i := startOffset; i < len(raw)-2; i++ {
		if raw[i] != 0x03 { // NDEF Message TLV type
			continue
		}

		// Parse TLV length field (1-byte or 3-byte form).
		var msgLen, msgStart int
		if raw[i+1] == 0xFF {
			if i+4 > len(raw) {
				continue
			}
			msgLen = int(raw[i+2])<<8 | int(raw[i+3])
			msgStart = i + 4
		} else {
			msgLen = int(raw[i+1])
			msgStart = i + 2
		}
		if msgLen == 0 || msgStart+msgLen > len(raw) {
			continue
		}

		// Walk NDEF records inside the message.
		pos := msgStart
		end := msgStart + msgLen
		for pos < end {
			if pos >= len(raw) {
				break
			}
			header := raw[pos]
			pos++

			if pos >= len(raw) {
				break
			}
			typeLen := int(raw[pos])
			pos++

			// Payload length: 1 byte when SR flag (bit 4) is set, 4 bytes otherwise.
			var payloadLen int
			if header&0x10 != 0 {
				if pos >= len(raw) {
					break
				}
				payloadLen = int(raw[pos])
				pos++
			} else {
				if pos+4 > len(raw) {
					break
				}
				payloadLen = int(raw[pos])<<24 | int(raw[pos+1])<<16 | int(raw[pos+2])<<8 | int(raw[pos+3])
				pos += 4
			}

			// Skip optional ID field when IL flag (bit 3) is set.
			if header&0x08 != 0 {
				if pos >= len(raw) {
					break
				}
				idLen := int(raw[pos])
				pos++
				pos += idLen
			}

			if pos+typeLen > len(raw) {
				break
			}
			recordType := string(raw[pos : pos+typeLen])
			pos += typeLen

			if pos+payloadLen > len(raw) {
				break
			}
			payload := raw[pos : pos+payloadLen]
			pos += payloadLen

			// Text record: strip status byte + language code, return bare text.
			if recordType == "T" && len(payload) > 1 {
				langLen := int(payload[0] & 0x3F) // lower 6 bits = lang code length
				if 1+langLen >= len(payload) {
					continue
				}
				text := strings.TrimRight(string(payload[1+langLen:]), "\x00")
				if text != "" {
					return text
				}
			}
		}
	}
	return ""
}

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

// Decode decodes a Branca token offline given a token string + SNR decimal.
// Key derivation: hex(MD5(string(snr_decimal))) → 32-byte Branca key.
func (h *CardHandlers) Decode(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/decode")
	var req models.CardDecodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if req.BrancaToken == "" {
		respondError(w, "branca_token is required", http.StatusBadRequest)
		return
	}

	key := snrToBrancaKey(req.SNRDecimal)
	log.Printf("[API] Decode: snr=%d key=%s", req.SNRDecimal, key)

	b := branca.NewBranca(key)
	payload, err := b.DecodeToString(req.BrancaToken)
	if err != nil {
		log.Printf("[API] Branca decode failed: %v", err)
		respondWithError(w, "Branca decode failed", err.Error(), http.StatusUnprocessableEntity, false)
		return
	}

	var parsed interface{}
	if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr != nil {
		// Payload is not JSON — return it as a plain string
		parsed = payload
	}

	resp := models.Response{
		Success: true,
		Message: "Branca token decoded successfully",
		Data: models.CardDecodeResponse{
			SNRDecimal:  req.SNRDecimal,
			BrancaToken: req.BrancaToken,
			Payload:     parsed,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ReadDecoded reads all card blocks, extracts the Branca token, decodes it, and
// returns the JSON payload — all in a single request.
func (h *CardHandlers) ReadDecoded(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/read-decoded")
	var req models.CardReadDecodedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Detect card
	log.Printf("[API] ReadDecoded: detecting card (mode=%d)...", req.Mode)
	snr, err := h.Reader.DetectCard(req.Mode)
	if err != nil {
		diagErr := parseDLLError(err.Error(), "dc_request")
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no card detected") || strings.Contains(err.Error(), "no card present") {
			statusCode = http.StatusNotFound
			diagErr.Suggestion = "No card detected. Place card on reader."
		}
		respondWithDiagnosticError(w, diagErr, statusCode)
		return
	}
	log.Printf("[API] Card detected: SNR=%08X (%d)", snr, snr)

	// Step 2: Derive Branca key from SNR decimal (MD5 of decimal string)
	brancaKey := snrToBrancaKey(snr)
	log.Printf("[API] ReadDecoded: SNR decimal=%d → branca key=%s", snr, brancaKey)

	// Step 3: Read all 64 blocks using a sector-based loop.
	// For each sector, try the user-provided key first, then well-known NDEF keys:
	//   D3F7D3F7D3F7 — NFC Forum NDEF data sectors (1-15, written by Android apps)
	//   A0A1A2A3A4A5 — NFC Forum MAD / sector 0 key
	//   FFFFFFFFFFFF — factory default
	// After a failed authentication Mifare Classic deselects the card, so we
	// re-detect before trying the next key.
	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{key} // user-provided key first
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	rawBytes := make([]byte, 0, 1024)
	for sector := 0; sector < 16; sector++ {
		block0 := sector * 4

		// Attempt authentication using LoadKey+Authenticate (standard 2-step protocol).
		// dc_authentication_pass returns 0 for some wrong keys (false positive), so we
		// verify each attempt by trying an actual dc_read of the first block.
		authenticated := false
		var block0Data [16]byte

		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] ReadDecoded: sector %d key[%d]=%X — LoadKey+Auth", sector, keyIdx, tryKey)

			// dc_load_key just writes to the reader's RAM; no card interaction, no re-detect needed on failure.
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] ReadDecoded: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}

			// dc_authentication does the crypto challenge. On failure the card deselects.
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] ReadDecoded: sector %d Authenticate failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}

			// Confirm authentication actually worked — a wrong key can still return 0 from Authenticate.
			data, readErr := h.Reader.ReadBlock(block0)
			if readErr != nil {
				log.Printf("[API] ReadDecoded: sector %d auth returned OK but read failed (%v), re-detecting", sector, readErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}

			log.Printf("[API] ReadDecoded: sector %d authenticated + verified with key[%d]", sector, keyIdx)
			authenticated = true
			block0Data = data
			break
		}

		// Sector 0 includes all 4 blocks (UID/MFG + MAD data + trailer) to keep startOffset=64 alignment.
		// Sectors 1-15 skip the sector trailer (block 3) — it holds access keys/bits, not NDEF data,
		// and would corrupt the Branca token bytes if included.
		blocksToRead := 4
		if sector > 0 {
			blocksToRead = 3
		}
		sectorPad := blocksToRead * 16

		if !authenticated {
			log.Printf("[API] ReadDecoded: sector %d: all keys exhausted, padding %d zeros", sector, sectorPad)
			rawBytes = append(rawBytes, make([]byte, sectorPad)...)
			continue
		}

		// First block already read during auth verification
		rawBytes = append(rawBytes, block0Data[:]...)

		// Read remaining data blocks (skip trailer for sectors 1-15)
		for blockInSector := 1; blockInSector < blocksToRead; blockInSector++ {
			blockAddr := block0 + blockInSector
			data, err := h.Reader.ReadBlock(blockAddr)
			if err != nil {
				log.Printf("[API] ReadDecoded: ReadBlock %d failed: %v", blockAddr, err)
				rawBytes = append(rawBytes, make([]byte, 16)...)
			} else {
				rawBytes = append(rawBytes, data[:]...)
			}
		}
	}
	h.Reader.Halt()

	// Count how many blocks had actual data (non-zero)
	nonZeroBlocks := 0
	for i := 0; i < len(rawBytes); i += 16 {
		end := i + 16
		if end > len(rawBytes) {
			end = len(rawBytes)
		}
		for _, b := range rawBytes[i:end] {
			if b != 0 {
				nonZeroBlocks++
				break
			}
		}
	}
	// Log first 64 bytes as hex for diagnosis
	preview := rawBytes
	if len(preview) > 64 {
		preview = preview[:64]
	}
	log.Printf("[API] ReadDecoded: rawBytes=%d bytes, non-zero blocks=%d, first64=%X", len(rawBytes), nonZeroBlocks, preview)

	// Extract Branca token from NDEF text record in raw card bytes
	token := extractNDEFText(rawBytes)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":          false,
			"error":            "No Branca token found",
			"message":          "Could not find a base62 token in the card data",
			"retryable":        false,
			"blocks_read":      nonZeroBlocks,
			"raw_hex_preview":  fmt.Sprintf("%X", preview),
		})
		return
	}
	log.Printf("[API] ReadDecoded: extracted token (len=%d): %.20s...", len(token), token)

	// Step 4: Decode the Branca token
	b := branca.NewBranca(brancaKey)
	payload, err := b.DecodeToString(token)
	if err != nil {
		log.Printf("[API] ReadDecoded: Branca decode failed: %v", err)
		respondWithError(w, "Branca decode failed", err.Error(), http.StatusUnprocessableEntity, false)
		return
	}

	var parsed interface{}
	if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr != nil {
		parsed = payload
	}

	resp := models.Response{
		Success: true,
		Message: "Card read and decoded successfully",
		Data: models.CardReadDecodedResponse{
			SNRHex:      fmt.Sprintf("0x%08X", snr),
			SNRDecimal:  snr,
			BrancaToken: token,
			Payload:     parsed,
		},
	}
	w.Header().Set("Content-Type", "application/json")
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
