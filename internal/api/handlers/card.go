package handlers

import (
	"crypto/md5"
	"crypto/sha256"
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

// snrToBrancaKeySHA derives a 32-byte Branca key using SHA256(snr_decimal + salt).
// SHA256 produces exactly 32 bytes, used directly as the XChaCha20-Poly1305 key.
func snrToBrancaKeySHA(snr uint32, salt string) string {
	snrStr := strconv.FormatUint(uint64(snr), 10)
	sum := sha256.Sum256([]byte(snrStr + salt))
	return string(sum[:]) // 32 raw bytes
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

// buildNDEFText encodes text into a raw NDEF TLV payload ready to write starting at block 5.
// Output format: 03 [length] [NDEF text record] FE
// The NDEF text record payload is: [status byte 0x02][lang "en"][text].
func buildNDEFText(text string) []byte {
	// NDEF text record payload: status(1) + lang(2) + text
	lang := "en"
	ndefPayload := make([]byte, 0, 1+len(lang)+len(text))
	ndefPayload = append(ndefPayload, byte(len(lang))) // status byte: UTF-8, langLen=2 → 0x02
	ndefPayload = append(ndefPayload, []byte(lang)...)
	ndefPayload = append(ndefPayload, []byte(text)...)

	// NDEF record: header depends on SR (short record) flag.
	// SR=1 when payload length ≤ 255, payload length is 1 byte (header 0xD1).
	// SR=0 when payload length > 255, payload length is 4 bytes big-endian (header 0xC1).
	var record []byte
	if len(ndefPayload) <= 255 {
		record = []byte{0xD1, 0x01, byte(len(ndefPayload)), 'T'}
	} else {
		pLen := uint32(len(ndefPayload))
		record = []byte{
			0xC1, 0x01,
			byte(pLen >> 24), byte(pLen >> 16), byte(pLen >> 8), byte(pLen),
			'T',
		}
	}
	record = append(record, ndefPayload...)

	// NDEF Message TLV: type=0x03, length (1 or 3 bytes), message, terminator 0xFE
	msgLen := len(record)
	var tlv []byte
	tlv = append(tlv, 0x03)
	if msgLen <= 254 {
		tlv = append(tlv, byte(msgLen))
	} else {
		tlv = append(tlv, 0xFF, byte(msgLen>>8), byte(msgLen))
	}
	tlv = append(tlv, record...)
	tlv = append(tlv, 0xFE)
	return tlv
}

// CardHandlers holds dependencies for card-related handlers
type CardHandlers struct {
	Reader *reader.Reader
	Salt   string // salt for SHA-based Branca key derivation (from BRANCA_SALT env)
}

// NewCardHandlers creates a new CardHandlers instance
func NewCardHandlers(r *reader.Reader, salt string) *CardHandlers {
	return &CardHandlers{Reader: r, Salt: salt}
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

// DecodeMD5 decodes a Branca token offline given a token string + SNR decimal.
// Key derivation: hex(MD5(string(snr_decimal))) → 32-byte Branca key.
func (h *CardHandlers) DecodeMD5(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/decode-md5")
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

// ReadDecodedMD5 reads all card blocks, extracts the Branca token, decodes it, and
// returns the JSON payload — all in a single request.
// Key derivation: hex(MD5(string(snr_decimal))) → 32-byte Branca key.
func (h *CardHandlers) ReadDecodedMD5(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/read-decoded-md5")
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
	log.Printf("[API] ReadDecoded: raw decoded payload (len=%d): %s", len(payload), payload)

	var parsed interface{}
	if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr != nil {
		log.Printf("[API] ReadDecoded: payload is not JSON, returning as string: %v", jsonErr)
		parsed = payload
	}

	resp := models.Response{
		Success: true,
		Message: "Card read and decoded successfully",
		Data:    parsed,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// WriteEncodedMD5 encodes any JSON value as a Branca token (key = hex(MD5(snr_decimal)))
// and writes it to the card as an NDEF text record starting at sector 1.
func (h *CardHandlers) WriteEncodedMD5(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/write-encoded-md5")
	var req models.CardWriteEncodedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if len(req.Data) == 0 || string(req.Data) == "null" {
		respondError(w, "data field is required and must be a valid JSON value", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Detect card → get SNR
	log.Printf("[API] WriteEncoded: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] WriteEncoded: card detected SNR=%08X (%d)", snr, snr)

	// Derive Branca key from SNR decimal (same derivation as ReadDecoded)
	brancaKey := snrToBrancaKey(snr)
	log.Printf("[API] WriteEncoded: SNR decimal=%d → branca key=%s", snr, brancaKey)

	// Encode JSON data as Branca token
	b := branca.NewBranca(brancaKey)
	token, encErr := b.EncodeToString(string(req.Data))
	if encErr != nil {
		log.Printf("[API] WriteEncoded: Branca encode failed: %v", encErr)
		respondWithError(w, "Branca encode failed", encErr.Error(), http.StatusInternalServerError, false)
		return
	}
	log.Printf("[API] WriteEncoded: branca token len=%d: %.30s...", len(token), token)

	// Build NDEF TLV payload
	ndefTLV := buildNDEFText(token)
	log.Printf("[API] WriteEncoded: NDEF TLV size=%d bytes", len(ndefTLV))

	// Sector 1 block 0 (block 4) = Capability Container; blocks 5,6 = NDEF data (32 bytes).
	// Sectors 2-15: 3 data blocks each = 42 × 16 = 672 bytes. Total NDEF data: 704 bytes.
	const maxNDEFBytes = 704
	if len(ndefTLV) > maxNDEFBytes {
		respondWithError(w, "Data too large",
			fmt.Sprintf("NDEF payload is %d bytes; card capacity is %d bytes. Reduce the JSON payload size.", len(ndefTLV), maxNDEFBytes),
			http.StatusBadRequest, false)
		return
	}

	// Pad NDEF TLV to fill all 704 bytes (zero-fill remaining blocks)
	ndefPadded := make([]byte, maxNDEFBytes)
	copy(ndefPadded, ndefTLV)

	// Capability Container for block 4: NDEF magic E1, version 1.0, size, read/write
	var ccBlock [16]byte
	ccBlock[0] = 0xE1
	ccBlock[1] = 0x10
	ccBlock[2] = 0x6D // 109 × 8 = 872 bytes declared capacity
	ccBlock[3] = 0x00

	// Try the user-provided key plus well-known NDEF keys (same strategy as ReadDecoded)
	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{key}
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	blocksWritten := 0
	ndefOffset := 0
	failedSectors := []int{}

	for sector := 1; sector < 16; sector++ {
		block0 := sector * 4

		// Authenticate sector (try each key, re-detect on failure)
		authenticated := false
		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] WriteEncoded: sector %d key[%d] — LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] WriteEncoded: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] WriteEncoded: sector %d Auth failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] WriteEncoded: sector %d authenticated with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] WriteEncoded: sector %d all keys failed, skipping", sector)
			failedSectors = append(failedSectors, sector)
			// Advance offset past this sector's data blocks
			blocksToSkip := 3
			if sector == 1 {
				blocksToSkip = 2 // block 4 = CC, NDEF in blocks 5,6
			}
			ndefOffset += blocksToSkip * 16
			continue
		}

		// Sector 1: write CC to block 4, NDEF data to blocks 5 and 6
		// Sectors 2-15: write NDEF data to blocks 0, 1, 2 (skip trailer block 3)
		startBlockInSector := 0
		if sector == 1 {
			if writeErr := h.Reader.WriteBlock(block0, ccBlock); writeErr != nil {
				log.Printf("[API] WriteEncoded: CC write to block %d failed: %v", block0, writeErr)
			} else {
				log.Printf("[API] WriteEncoded: CC written to block %d", block0)
			}
			startBlockInSector = 1 // NDEF starts at block 5
		}

		for blockInSector := startBlockInSector; blockInSector < 3; blockInSector++ {
			blockAddr := block0 + blockInSector
			var blockData [16]byte
			if ndefOffset < len(ndefPadded) {
				copy(blockData[:], ndefPadded[ndefOffset:])
			}
			ndefOffset += 16

			if writeErr := h.Reader.WriteBlock(blockAddr, blockData); writeErr != nil {
				log.Printf("[API] WriteEncoded: WriteBlock %d failed: %v", blockAddr, writeErr)
			} else {
				log.Printf("[API] WriteEncoded: block %d written", blockAddr)
				blocksWritten++
			}
		}
	}

	h.Reader.Halt()

	success := len(failedSectors) == 0
	msg := fmt.Sprintf("Card encoded and written successfully. %d blocks written.", blocksWritten)
	if !success {
		msg = fmt.Sprintf("Write completed with errors. %d blocks written, failed sectors: %v", blocksWritten, failedSectors)
	}

	resp := models.Response{
		Success: success,
		Message: msg,
		Data: models.CardWriteEncodedResponse{
			SNRHex:        fmt.Sprintf("0x%08X", snr),
			SNRDecimal:    snr,
			BrancaToken:   token,
			BytesWritten:  blocksWritten * 16,
			BlocksWritten: blocksWritten,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// DecodeSHA decodes a Branca token offline given a token string + SNR decimal.
// Key derivation: SHA256(snr_decimal + BRANCA_SALT) → 32-byte Branca key.
func (h *CardHandlers) DecodeSHA(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/decode-sha")
	var req models.CardDecodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if req.BrancaToken == "" {
		respondError(w, "branca_token is required", http.StatusBadRequest)
		return
	}

	key := snrToBrancaKeySHA(req.SNRDecimal, h.Salt)
	log.Printf("[API] DecodeSHA: snr=%d key derived via SHA256+salt", req.SNRDecimal)

	b := branca.NewBranca(key)
	payload, err := b.DecodeToString(req.BrancaToken)
	if err != nil {
		log.Printf("[API] DecodeSHA: Branca decode failed: %v", err)
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

// ReadDecodedSHA reads all card blocks, extracts the Branca token, decodes it, and
// returns the JSON payload — all in a single request.
// Key derivation: SHA256(snr_decimal + BRANCA_SALT) → 32-byte Branca key.
func (h *CardHandlers) ReadDecodedSHA(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/read-decoded-sha")
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
	log.Printf("[API] ReadDecodedSHA: detecting card (mode=%d)...", req.Mode)
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

	// Derive Branca key from SNR decimal using SHA256 + salt
	brancaKey := snrToBrancaKeySHA(snr, h.Salt)
	log.Printf("[API] ReadDecodedSHA: SNR decimal=%d → branca key derived via SHA256+salt", snr)

	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{key}
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	rawBytes := make([]byte, 0, 1024)
	for sector := 0; sector < 16; sector++ {
		block0 := sector * 4

		authenticated := false
		var block0Data [16]byte

		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] ReadDecodedSHA: sector %d key[%d]=%X — LoadKey+Auth", sector, keyIdx, tryKey)

			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] ReadDecodedSHA: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}

			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] ReadDecodedSHA: sector %d Authenticate failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}

			data, readErr := h.Reader.ReadBlock(block0)
			if readErr != nil {
				log.Printf("[API] ReadDecodedSHA: sector %d auth returned OK but read failed (%v), re-detecting", sector, readErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}

			log.Printf("[API] ReadDecodedSHA: sector %d authenticated + verified with key[%d]", sector, keyIdx)
			authenticated = true
			block0Data = data
			break
		}

		blocksToRead := 4
		if sector > 0 {
			blocksToRead = 3
		}
		sectorPad := blocksToRead * 16

		if !authenticated {
			log.Printf("[API] ReadDecodedSHA: sector %d: all keys exhausted, padding %d zeros", sector, sectorPad)
			rawBytes = append(rawBytes, make([]byte, sectorPad)...)
			continue
		}

		rawBytes = append(rawBytes, block0Data[:]...)

		for blockInSector := 1; blockInSector < blocksToRead; blockInSector++ {
			blockAddr := block0 + blockInSector
			data, err := h.Reader.ReadBlock(blockAddr)
			if err != nil {
				log.Printf("[API] ReadDecodedSHA: ReadBlock %d failed: %v", blockAddr, err)
				rawBytes = append(rawBytes, make([]byte, 16)...)
			} else {
				rawBytes = append(rawBytes, data[:]...)
			}
		}
	}
	h.Reader.Halt()

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
	preview := rawBytes
	if len(preview) > 64 {
		preview = preview[:64]
	}
	log.Printf("[API] ReadDecodedSHA: rawBytes=%d bytes, non-zero blocks=%d, first64=%X", len(rawBytes), nonZeroBlocks, preview)

	token := extractNDEFText(rawBytes)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         false,
			"error":           "No Branca token found",
			"message":         "Could not find a base62 token in the card data",
			"retryable":       false,
			"blocks_read":     nonZeroBlocks,
			"raw_hex_preview": fmt.Sprintf("%X", preview),
		})
		return
	}
	log.Printf("[API] ReadDecodedSHA: extracted token (len=%d): %.20s...", len(token), token)

	b := branca.NewBranca(brancaKey)
	payload, err := b.DecodeToString(token)
	if err != nil {
		log.Printf("[API] ReadDecodedSHA: Branca decode failed: %v", err)
		respondWithError(w, "Branca decode failed", err.Error(), http.StatusUnprocessableEntity, false)
		return
	}
	log.Printf("[API] ReadDecodedSHA: raw decoded payload (len=%d): %s", len(payload), payload)

	var parsed interface{}
	if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr != nil {
		log.Printf("[API] ReadDecodedSHA: payload is not JSON, returning as string: %v", jsonErr)
		parsed = payload
	}

	resp := models.Response{
		Success: true,
		Message: "Card read and decoded successfully",
		Data:    parsed,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// WriteEncodedSHA encodes any JSON value as a Branca token (key = SHA256(snr_decimal + BRANCA_SALT))
// and writes it to the card as an NDEF text record starting at sector 1.
func (h *CardHandlers) WriteEncodedSHA(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/write-encoded-sha")
	var req models.CardWriteEncodedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if len(req.Data) == 0 || string(req.Data) == "null" {
		respondError(w, "data field is required and must be a valid JSON value", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Detect card → get SNR
	log.Printf("[API] WriteEncodedSHA: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] WriteEncodedSHA: card detected SNR=%08X (%d)", snr, snr)

	// Derive Branca key from SNR decimal using SHA256 + salt
	brancaKey := snrToBrancaKeySHA(snr, h.Salt)
	log.Printf("[API] WriteEncodedSHA: SNR decimal=%d → branca key derived via SHA256+salt", snr)

	// Encode JSON data as Branca token
	b := branca.NewBranca(brancaKey)
	token, encErr := b.EncodeToString(string(req.Data))
	if encErr != nil {
		log.Printf("[API] WriteEncodedSHA: Branca encode failed: %v", encErr)
		respondWithError(w, "Branca encode failed", encErr.Error(), http.StatusInternalServerError, false)
		return
	}
	log.Printf("[API] WriteEncodedSHA: branca token len=%d: %.30s...", len(token), token)

	// Build NDEF TLV payload
	ndefTLV := buildNDEFText(token)
	log.Printf("[API] WriteEncodedSHA: NDEF TLV size=%d bytes", len(ndefTLV))

	const maxNDEFBytes = 704
	if len(ndefTLV) > maxNDEFBytes {
		respondWithError(w, "Data too large",
			fmt.Sprintf("NDEF payload is %d bytes; card capacity is %d bytes. Reduce the JSON payload size.", len(ndefTLV), maxNDEFBytes),
			http.StatusBadRequest, false)
		return
	}

	ndefPadded := make([]byte, maxNDEFBytes)
	copy(ndefPadded, ndefTLV)

	var ccBlock [16]byte
	ccBlock[0] = 0xE1
	ccBlock[1] = 0x10
	ccBlock[2] = 0x6D
	ccBlock[3] = 0x00

	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{key}
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	blocksWritten := 0
	ndefOffset := 0
	failedSectors := []int{}

	for sector := 1; sector < 16; sector++ {
		block0 := sector * 4

		authenticated := false
		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] WriteEncodedSHA: sector %d key[%d] — LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] WriteEncodedSHA: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] WriteEncodedSHA: sector %d Auth failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] WriteEncodedSHA: sector %d authenticated with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] WriteEncodedSHA: sector %d all keys failed, skipping", sector)
			failedSectors = append(failedSectors, sector)
			blocksToSkip := 3
			if sector == 1 {
				blocksToSkip = 2
			}
			ndefOffset += blocksToSkip * 16
			continue
		}

		startBlockInSector := 0
		if sector == 1 {
			if writeErr := h.Reader.WriteBlock(block0, ccBlock); writeErr != nil {
				log.Printf("[API] WriteEncodedSHA: CC write to block %d failed: %v", block0, writeErr)
			} else {
				log.Printf("[API] WriteEncodedSHA: CC written to block %d", block0)
			}
			startBlockInSector = 1
		}

		for blockInSector := startBlockInSector; blockInSector < 3; blockInSector++ {
			blockAddr := block0 + blockInSector
			var blockData [16]byte
			if ndefOffset < len(ndefPadded) {
				copy(blockData[:], ndefPadded[ndefOffset:])
			}
			ndefOffset += 16

			if writeErr := h.Reader.WriteBlock(blockAddr, blockData); writeErr != nil {
				log.Printf("[API] WriteEncodedSHA: WriteBlock %d failed: %v", blockAddr, writeErr)
			} else {
				log.Printf("[API] WriteEncodedSHA: block %d written", blockAddr)
				blocksWritten++
			}
		}
	}

	h.Reader.Halt()

	success := len(failedSectors) == 0
	msg := fmt.Sprintf("Card encoded and written successfully. %d blocks written.", blocksWritten)
	if !success {
		msg = fmt.Sprintf("Write completed with errors. %d blocks written, failed sectors: %v", blocksWritten, failedSectors)
	}

	resp := models.Response{
		Success: success,
		Message: msg,
		Data: models.CardWriteEncodedResponse{
			SNRHex:        fmt.Sprintf("0x%08X", snr),
			SNRDecimal:    snr,
			BrancaToken:   token,
			BytesWritten:  blocksWritten * 16,
			BlocksWritten: blocksWritten,
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
