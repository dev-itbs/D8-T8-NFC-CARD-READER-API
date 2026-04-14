package handlers

import (
	"crypto/md5"
	"crypto/sha256"
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
//
//	sector 0  (blocks 0-3)  – UID/MFG + MAD (contains false 0x03 bytes)
//	sector 1+ (blocks 4-63) – CC (E1 10 ...) followed by NDEF TLV: 03 [len] [records]
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

// Detect godoc
//
//	@Summary		Detect NFC card
//	@Description	Detects an NFC card in the RF field and returns its serial number
//	@Tags			Card Operations
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardDetectRequest						true	"Detect request"
//	@Success		200		{object}	models.Response{data=models.CardDetectResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/detect [post]
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

// Halt godoc
//
//	@Summary		Halt card
//	@Description	Deselects the card from the RF field
//	@Tags			Card Operations
//	@Produce		json
//	@Success		200	{object}	models.Response
//	@Failure		500	{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/halt [post]
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

// ReadDecodedMD5 godoc
//
//	@Summary		Read and decode card (MD5)
//	@Description	Reads all card blocks, extracts the NDEF Branca token, and decodes it using a key derived from hex(MD5(snr_decimal))
//	@Tags			MD5 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardReadDecodedRequest						true	"Read request"
//	@Success		200		{object}	models.Response{data=models.CardReadDecodedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/1/read-decoded [post]
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
			"success":         false,
			"error":           "No Branca token found",
			"message":         "Could not find a base62 token in the card data",
			"retryable":       false,
			"blocks_read":     nonZeroBlocks,
			"raw_hex_preview": fmt.Sprintf("%X", preview),
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

// WriteEncodedMD5 godoc
//
//	@Summary		Encode and write to card (MD5)
//	@Description	Encodes JSON data as a Branca token using key=hex(MD5(snr_decimal)) and writes it as an NDEF text record to the card
//	@Tags			MD5 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardWriteEncodedRequest						true	"Write request"
//	@Success		200		{object}	models.Response{data=models.CardWriteEncodedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/1/write-encoded [post]
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

// ReadDecodedSHA godoc
//
//	@Summary		Read and decode card (SHA256)
//	@Description	Reads all card blocks, extracts the NDEF Branca token, and decodes it using a key derived from SHA256(snr_decimal + BRANCA_SALT)
//	@Tags			SHA256 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardReadDecodedRequest						true	"Read request"
//	@Success		200		{object}	models.Response{data=models.CardReadDecodedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/2/read-decoded [post]
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

// WriteEncodedSHA godoc
//
//	@Summary		Encode and write to card (SHA256)
//	@Description	Encodes JSON data as a Branca token using key=SHA256(snr_decimal+BRANCA_SALT) and writes it as an NDEF text record to the card
//	@Tags			SHA256 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardWriteEncodedRequest						true	"Write request"
//	@Success		200		{object}	models.Response{data=models.CardWriteEncodedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/2/write-encoded [post]
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

// ReadDecodedLockedMD5 godoc
//
//	@Summary		Read and decode locked card (MD5)
//	@Description	Reads all blocks from a passkey-locked card, derives the Mifare sector key from the passkey (MD5(passkey)[0:6]), authenticates, extracts the NDEF Branca token, and decodes it using a key derived from hex(MD5(snr_decimal)). Use this endpoint instead of read-decoded when the card was written with write-encoded-locked.
//	@Tags			MD5 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardReadDecodedLockedRequest					true	"Read request with passkey"
//	@Success		200		{object}	models.Response{data=models.CardReadDecodedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/1/read-decoded-locked [post]
func (h *CardHandlers) ReadDecodedLockedMD5(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/1/read-decoded-locked")
	var req models.CardReadDecodedLockedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if strings.TrimSpace(req.Passkey) == "" {
		respondError(w, "passkey is required to read a locked card", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Derive the 6-byte Mifare key that was set during the locked write
	passkeyMifareKey, passkeyKeyHex := passkeyToMifareKey(req.Passkey)
	log.Printf("[API] ReadDecodedLockedMD5: passkey → Mifare key %s", passkeyKeyHex)

	// Detect card
	log.Printf("[API] ReadDecodedLockedMD5: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] ReadDecodedLockedMD5: card detected SNR=%08X (%d)", snr, snr)

	// Derive Branca key from SNR decimal (MD5 method — same as write-encoded-locked for MD5)
	brancaKey := snrToBrancaKey(snr)
	log.Printf("[API] ReadDecodedLockedMD5: SNR decimal=%d → branca key=%s", snr, brancaKey)

	// Try passkey-derived key first, then user key, then NDEF defaults
	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{passkeyMifareKey, key}
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
			log.Printf("[API] ReadDecodedLockedMD5: sector %d key[%d]=%X — LoadKey+Auth", sector, keyIdx, tryKey)

			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] ReadDecodedLockedMD5: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] ReadDecodedLockedMD5: sector %d Authenticate failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			data, readErr := h.Reader.ReadBlock(block0)
			if readErr != nil {
				log.Printf("[API] ReadDecodedLockedMD5: sector %d auth OK but read failed (%v), re-detecting", sector, readErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			log.Printf("[API] ReadDecodedLockedMD5: sector %d authenticated + verified with key[%d]", sector, keyIdx)
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
			log.Printf("[API] ReadDecodedLockedMD5: sector %d: all keys exhausted, padding %d zeros", sector, sectorPad)
			rawBytes = append(rawBytes, make([]byte, sectorPad)...)
			continue
		}

		rawBytes = append(rawBytes, block0Data[:]...)
		for blockInSector := 1; blockInSector < blocksToRead; blockInSector++ {
			blockAddr := block0 + blockInSector
			data, err := h.Reader.ReadBlock(blockAddr)
			if err != nil {
				log.Printf("[API] ReadDecodedLockedMD5: ReadBlock %d failed: %v", blockAddr, err)
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
	log.Printf("[API] ReadDecodedLockedMD5: rawBytes=%d bytes, non-zero blocks=%d, first64=%X", len(rawBytes), nonZeroBlocks, preview)

	token := extractNDEFText(rawBytes)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         false,
			"error":           "No Branca token found",
			"message":         "Could not find a base62 token in the card data. Check that the passkey is correct and the card was written with write-encoded-locked.",
			"retryable":       false,
			"blocks_read":     nonZeroBlocks,
			"raw_hex_preview": fmt.Sprintf("%X", preview),
		})
		return
	}
	log.Printf("[API] ReadDecodedLockedMD5: extracted token (len=%d): %.20s...", len(token), token)

	b := branca.NewBranca(brancaKey)
	payload, err := b.DecodeToString(token)
	if err != nil {
		log.Printf("[API] ReadDecodedLockedMD5: Branca decode failed: %v", err)
		respondWithError(w, "Branca decode failed", err.Error(), http.StatusUnprocessableEntity, false)
		return
	}
	log.Printf("[API] ReadDecodedLockedMD5: raw decoded payload (len=%d): %s", len(payload), payload)

	var parsed interface{}
	if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr != nil {
		log.Printf("[API] ReadDecodedLockedMD5: payload is not JSON, returning as string: %v", jsonErr)
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

// ReadDecodedLockedSHA godoc
//
//	@Summary		Read and decode locked card (SHA256)
//	@Description	Reads all blocks from a passkey-locked card, derives the Mifare sector key from the passkey (MD5(passkey)[0:6]), authenticates, extracts the NDEF Branca token, and decodes it using a key derived from SHA256(snr_decimal+BRANCA_SALT). Use this endpoint instead of read-decoded when the card was written with write-encoded-locked (SHA256 variant).
//	@Tags			SHA256 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardReadDecodedLockedRequest					true	"Read request with passkey"
//	@Success		200		{object}	models.Response{data=models.CardReadDecodedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/2/read-decoded-locked [post]
func (h *CardHandlers) ReadDecodedLockedSHA(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/2/read-decoded-locked")
	var req models.CardReadDecodedLockedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if strings.TrimSpace(req.Passkey) == "" {
		respondError(w, "passkey is required to read a locked card", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Derive the 6-byte Mifare key that was set during the locked write
	passkeyMifareKey, passkeyKeyHex := passkeyToMifareKey(req.Passkey)
	log.Printf("[API] ReadDecodedLockedSHA: passkey → Mifare key %s", passkeyKeyHex)

	// Detect card
	log.Printf("[API] ReadDecodedLockedSHA: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] ReadDecodedLockedSHA: card detected SNR=%08X (%d)", snr, snr)

	// Derive Branca key using SHA256 + salt (same as write-encoded-locked for SHA256)
	brancaKey := snrToBrancaKeySHA(snr, h.Salt)
	log.Printf("[API] ReadDecodedLockedSHA: SNR decimal=%d → branca key derived via SHA256+salt", snr)

	// Try passkey-derived key first, then user key, then NDEF defaults
	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{passkeyMifareKey, key}
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
			log.Printf("[API] ReadDecodedLockedSHA: sector %d key[%d]=%X — LoadKey+Auth", sector, keyIdx, tryKey)

			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] ReadDecodedLockedSHA: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] ReadDecodedLockedSHA: sector %d Authenticate failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			data, readErr := h.Reader.ReadBlock(block0)
			if readErr != nil {
				log.Printf("[API] ReadDecodedLockedSHA: sector %d auth OK but read failed (%v), re-detecting", sector, readErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			log.Printf("[API] ReadDecodedLockedSHA: sector %d authenticated + verified with key[%d]", sector, keyIdx)
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
			log.Printf("[API] ReadDecodedLockedSHA: sector %d: all keys exhausted, padding %d zeros", sector, sectorPad)
			rawBytes = append(rawBytes, make([]byte, sectorPad)...)
			continue
		}

		rawBytes = append(rawBytes, block0Data[:]...)
		for blockInSector := 1; blockInSector < blocksToRead; blockInSector++ {
			blockAddr := block0 + blockInSector
			data, err := h.Reader.ReadBlock(blockAddr)
			if err != nil {
				log.Printf("[API] ReadDecodedLockedSHA: ReadBlock %d failed: %v", blockAddr, err)
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
	log.Printf("[API] ReadDecodedLockedSHA: rawBytes=%d bytes, non-zero blocks=%d, first64=%X", len(rawBytes), nonZeroBlocks, preview)

	token := extractNDEFText(rawBytes)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         false,
			"error":           "No Branca token found",
			"message":         "Could not find a base62 token in the card data. Check that the passkey is correct and the card was written with write-encoded-locked.",
			"retryable":       false,
			"blocks_read":     nonZeroBlocks,
			"raw_hex_preview": fmt.Sprintf("%X", preview),
		})
		return
	}
	log.Printf("[API] ReadDecodedLockedSHA: extracted token (len=%d): %.20s...", len(token), token)

	b := branca.NewBranca(brancaKey)
	payload, err := b.DecodeToString(token)
	if err != nil {
		log.Printf("[API] ReadDecodedLockedSHA: Branca decode failed: %v", err)
		respondWithError(w, "Branca decode failed", err.Error(), http.StatusUnprocessableEntity, false)
		return
	}
	log.Printf("[API] ReadDecodedLockedSHA: raw decoded payload (len=%d): %s", len(payload), payload)

	var parsed interface{}
	if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr != nil {
		log.Printf("[API] ReadDecodedLockedSHA: payload is not JSON, returning as string: %v", jsonErr)
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

// passkeyToMifareKey derives a 6-byte Mifare Classic sector key from an arbitrary passkey string.
// Derivation: key = MD5(passkey)[0:6]
// Returns both the raw 6-byte key and its uppercase hex representation (12 chars).
func passkeyToMifareKey(passkey string) ([6]byte, string) {
	hash := md5.Sum([]byte(passkey))
	var key [6]byte
	copy(key[:], hash[:6])
	return key, strings.ToUpper(hex.EncodeToString(key[:]))
}

// lockSectorTrailers attempts to change the sector key on all 15 data sectors (1-15).
// It builds a new 16-byte sector trailer:
//
//	bytes 0-5  : newKey (KEY A)
//	bytes 6-9  : 0x7F 0x07 0x88 0x40  (standard NDEF access bits)
//	bytes 10-15: newKey (KEY B — also replaced so KEY B = passkey-derived)
//
// Authentication is tried in two rounds per sector:
//  1. KEY B mode (4) — works on standard NDEF cards where only KEY B may write the trailer
//  2. KEY A mode (userKeyMode) — works on factory-fresh cards where KEY A can write the trailer
//
// Keys tried in each round: passkey-derived key, user-supplied key, then well-known NDEF defaults.
// Returns the number of sectors successfully locked (max 15).
func lockSectorTrailers(h *CardHandlers, mode, userKeyMode int, userKey, newKey [6]byte) int {
	ndefDefaults := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	lockKeys := [][6]byte{newKey, userKey}
	for _, kh := range ndefDefaults {
		k, _ := reader.KeyFromHex(kh)
		lockKeys = append(lockKeys, k)
	}

	// Standard NDEF access bits for sectors 1-15 (trailer C1=0,C2=1,C3=1):
	// KEY A: only KEY B can write it; ACCESS BITS: KEY A|B readable, KEY B writable
	// KEY B: not publicly readable → KEY B can still authenticate
	var trailer [16]byte
	copy(trailer[0:6], newKey[:])   // KEY A = passkey-derived
	trailer[6] = 0x7F               // \ NDEF standard
	trailer[7] = 0x07               //   access bits
	trailer[8] = 0x88               // /  for sectors 1-15
	trailer[9] = 0x40               // user byte
	copy(trailer[10:16], newKey[:]) // KEY B = passkey-derived

	locked := 0
	for sector := 1; sector < 16; sector++ {
		trailerAddr := sector*4 + 3
		sectorLocked := false

		// Round 1: try KEY B (mode 4) — required on NDEF-formatted cards
		for _, tryKey := range lockKeys {
			if loadErr := h.Reader.LoadKey(4, sector, tryKey); loadErr != nil {
				continue
			}
			if authErr := h.Reader.Authenticate(4, sector); authErr != nil {
				log.Printf("[LOCK] sector %d KEY-B auth failed (%v), re-detecting", sector, authErr)
				h.Reader.DetectCard(mode) //nolint:errcheck
				continue
			}
			if writeErr := h.Reader.WriteBlock(trailerAddr, trailer); writeErr != nil {
				log.Printf("[LOCK] sector %d trailer write (KEY-B) failed: %v", sector, writeErr)
				continue
			}
			log.Printf("[LOCK] sector %d locked via KEY-B", sector)
			sectorLocked = true
			break
		}

		// Round 2: try KEY A (userKeyMode) — works on factory-fresh cards
		if !sectorLocked {
			for _, tryKey := range lockKeys {
				if loadErr := h.Reader.LoadKey(userKeyMode, sector, tryKey); loadErr != nil {
					continue
				}
				if authErr := h.Reader.Authenticate(userKeyMode, sector); authErr != nil {
					log.Printf("[LOCK] sector %d KEY-A auth failed (%v), re-detecting", sector, authErr)
					h.Reader.DetectCard(mode) //nolint:errcheck
					continue
				}
				if writeErr := h.Reader.WriteBlock(trailerAddr, trailer); writeErr != nil {
					log.Printf("[LOCK] sector %d trailer write (KEY-A) failed: %v", sector, writeErr)
					continue
				}
				log.Printf("[LOCK] sector %d locked via KEY-A", sector)
				sectorLocked = true
				break
			}
		}

		if sectorLocked {
			locked++
		} else {
			log.Printf("[LOCK] sector %d could not be locked", sector)
		}
	}
	return locked
}

// WriteEncodedLockedMD5 godoc
//
//	@Summary		Encode, write, and lock card (MD5)
//	@Description	Encodes JSON data as a Branca token (key=hex(MD5(snr_decimal))), writes it as an NDEF text record, then locks all 15 data sectors by replacing their Mifare keys with a key derived from the passkey (MD5(passkey)[0:6]). After locking, the normal write-encoded endpoint cannot write to the card because it cannot authenticate with the default NDEF keys. To overwrite a locked card, call this endpoint again with the same passkey.
//	@Tags			MD5 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardWriteEncodedLockedRequest					true	"Write + lock request (passkey required)"
//	@Success		200		{object}	models.Response{data=models.CardWriteEncodedLockedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/1/write-encoded-locked [post]
func (h *CardHandlers) WriteEncodedLockedMD5(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/1/write-encoded-locked")
	var req models.CardWriteEncodedLockedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if len(req.Data) == 0 || string(req.Data) == "null" {
		respondError(w, "data field is required and must be a valid JSON value", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Passkey) == "" {
		respondError(w, "passkey is required for locked write", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Derive 6-byte Mifare key from passkey: MD5(passkey)[0:6]
	passkeyMifareKey, passkeyKeyHex := passkeyToMifareKey(req.Passkey)
	log.Printf("[API] WriteEncodedLockedMD5: passkey → Mifare key %s", passkeyKeyHex)

	// Detect card → get SNR
	log.Printf("[API] WriteEncodedLockedMD5: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] WriteEncodedLockedMD5: card detected SNR=%08X (%d)", snr, snr)

	// Derive Branca key from SNR decimal (MD5 method)
	brancaKey := snrToBrancaKey(snr)
	log.Printf("[API] WriteEncodedLockedMD5: SNR decimal=%d → branca key=%s", snr, brancaKey)

	// Encode JSON data as Branca token
	b := branca.NewBranca(brancaKey)
	token, encErr := b.EncodeToString(string(req.Data))
	if encErr != nil {
		log.Printf("[API] WriteEncodedLockedMD5: Branca encode failed: %v", encErr)
		respondWithError(w, "Branca encode failed", encErr.Error(), http.StatusInternalServerError, false)
		return
	}
	log.Printf("[API] WriteEncodedLockedMD5: branca token len=%d: %.30s...", len(token), token)

	// Build NDEF TLV payload
	ndefTLV := buildNDEFText(token)
	log.Printf("[API] WriteEncodedLockedMD5: NDEF TLV size=%d bytes", len(ndefTLV))

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

	// Keys to try for data-block authentication (KEY A): passkey key first, then user key, then NDEF defaults
	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{passkeyMifareKey, key}
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	// ── Phase 1: write data blocks (identical to WriteEncodedMD5) ──────────────
	blocksWritten := 0
	ndefOffset := 0
	failedSectors := []int{}

	for sector := 1; sector < 16; sector++ {
		block0 := sector * 4

		authenticated := false
		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] WriteEncodedLockedMD5: sector %d key[%d] — LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] WriteEncodedLockedMD5: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] WriteEncodedLockedMD5: sector %d Auth failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] WriteEncodedLockedMD5: sector %d authenticated with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] WriteEncodedLockedMD5: sector %d all keys failed, skipping", sector)
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
				log.Printf("[API] WriteEncodedLockedMD5: CC write to block %d failed: %v", block0, writeErr)
			} else {
				log.Printf("[API] WriteEncodedLockedMD5: CC written to block %d", block0)
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
				log.Printf("[API] WriteEncodedLockedMD5: WriteBlock %d failed: %v", blockAddr, writeErr)
			} else {
				log.Printf("[API] WriteEncodedLockedMD5: block %d written", blockAddr)
				blocksWritten++
			}
		}
	}
	h.Reader.Halt()

	// ── Phase 2: lock sector trailers with passkey-derived key ─────────────────
	log.Printf("[API] WriteEncodedLockedMD5: starting lock phase, passkey key=%s", passkeyKeyHex)
	if _, redetectErr := h.Reader.DetectCard(req.Mode); redetectErr != nil {
		log.Printf("[API] WriteEncodedLockedMD5: re-detect for lock phase failed: %v", redetectErr)
	}
	sectorsLocked := lockSectorTrailers(h, req.Mode, req.KeyMode, key, passkeyMifareKey)
	h.Reader.Halt()
	log.Printf("[API] WriteEncodedLockedMD5: locked %d/15 sectors", sectorsLocked)

	writeSuccess := len(failedSectors) == 0
	locked := sectorsLocked == 15
	msg := fmt.Sprintf("Card encoded, written, and locked. %d blocks written, %d/15 sectors locked.", blocksWritten, sectorsLocked)
	if !writeSuccess {
		msg = fmt.Sprintf("Write completed with errors. %d blocks written (failed sectors: %v), %d/15 sectors locked.", blocksWritten, failedSectors, sectorsLocked)
	}

	resp := models.Response{
		Success: writeSuccess,
		Message: msg,
		Data: models.CardWriteEncodedLockedResponse{
			SNRHex:        fmt.Sprintf("0x%08X", snr),
			SNRDecimal:    snr,
			BrancaToken:   token,
			BytesWritten:  blocksWritten * 16,
			BlocksWritten: blocksWritten,
			SectorsLocked: sectorsLocked,
			Locked:        locked,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// WriteEncodedLockedSHA godoc
//
//	@Summary		Encode, write, and lock card (SHA256)
//	@Description	Encodes JSON data as a Branca token (key=SHA256(snr_decimal+BRANCA_SALT)), writes it as an NDEF text record, then locks all 15 data sectors by replacing their Mifare keys with a key derived from the passkey (MD5(passkey)[0:6]). After locking, the normal write-encoded endpoint cannot write to the card because it cannot authenticate with the default NDEF keys. To overwrite a locked card, call this endpoint again with the same passkey.
//	@Tags			SHA256 Endpoints
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardWriteEncodedLockedRequest					true	"Write + lock request (passkey required)"
//	@Success		200		{object}	models.Response{data=models.CardWriteEncodedLockedResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/2/write-encoded-locked [post]
func (h *CardHandlers) WriteEncodedLockedSHA(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/2/write-encoded-locked")
	var req models.CardWriteEncodedLockedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if len(req.Data) == 0 || string(req.Data) == "null" {
		respondError(w, "data field is required and must be a valid JSON value", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Passkey) == "" {
		respondError(w, "passkey is required for locked write", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Derive 6-byte Mifare key from passkey: MD5(passkey)[0:6]
	passkeyMifareKey, passkeyKeyHex := passkeyToMifareKey(req.Passkey)
	log.Printf("[API] WriteEncodedLockedSHA: passkey → Mifare key %s", passkeyKeyHex)

	// Detect card → get SNR
	log.Printf("[API] WriteEncodedLockedSHA: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] WriteEncodedLockedSHA: card detected SNR=%08X (%d)", snr, snr)

	// Derive Branca key from SNR decimal using SHA256 + salt
	brancaKey := snrToBrancaKeySHA(snr, h.Salt)
	log.Printf("[API] WriteEncodedLockedSHA: SNR decimal=%d → branca key derived via SHA256+salt", snr)

	// Encode JSON data as Branca token
	b := branca.NewBranca(brancaKey)
	token, encErr := b.EncodeToString(string(req.Data))
	if encErr != nil {
		log.Printf("[API] WriteEncodedLockedSHA: Branca encode failed: %v", encErr)
		respondWithError(w, "Branca encode failed", encErr.Error(), http.StatusInternalServerError, false)
		return
	}
	log.Printf("[API] WriteEncodedLockedSHA: branca token len=%d: %.30s...", len(token), token)

	// Build NDEF TLV payload
	ndefTLV := buildNDEFText(token)
	log.Printf("[API] WriteEncodedLockedSHA: NDEF TLV size=%d bytes", len(ndefTLV))

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

	// Keys to try for data-block authentication (KEY A): passkey key first, then user key, then NDEF defaults
	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{passkeyMifareKey, key}
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	// ── Phase 1: write data blocks (identical to WriteEncodedSHA) ──────────────
	blocksWritten := 0
	ndefOffset := 0
	failedSectors := []int{}

	for sector := 1; sector < 16; sector++ {
		block0 := sector * 4

		authenticated := false
		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] WriteEncodedLockedSHA: sector %d key[%d] — LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] WriteEncodedLockedSHA: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] WriteEncodedLockedSHA: sector %d Auth failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] WriteEncodedLockedSHA: sector %d authenticated with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] WriteEncodedLockedSHA: sector %d all keys failed, skipping", sector)
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
				log.Printf("[API] WriteEncodedLockedSHA: CC write to block %d failed: %v", block0, writeErr)
			} else {
				log.Printf("[API] WriteEncodedLockedSHA: CC written to block %d", block0)
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
				log.Printf("[API] WriteEncodedLockedSHA: WriteBlock %d failed: %v", blockAddr, writeErr)
			} else {
				log.Printf("[API] WriteEncodedLockedSHA: block %d written", blockAddr)
				blocksWritten++
			}
		}
	}
	h.Reader.Halt()

	// ── Phase 2: lock sector trailers with passkey-derived key ─────────────────
	log.Printf("[API] WriteEncodedLockedSHA: starting lock phase, passkey key=%s", passkeyKeyHex)
	if _, redetectErr := h.Reader.DetectCard(req.Mode); redetectErr != nil {
		log.Printf("[API] WriteEncodedLockedSHA: re-detect for lock phase failed: %v", redetectErr)
	}
	sectorsLocked := lockSectorTrailers(h, req.Mode, req.KeyMode, key, passkeyMifareKey)
	h.Reader.Halt()
	log.Printf("[API] WriteEncodedLockedSHA: locked %d/15 sectors", sectorsLocked)

	writeSuccess := len(failedSectors) == 0
	locked := sectorsLocked == 15
	msg := fmt.Sprintf("Card encoded, written, and locked. %d blocks written, %d/15 sectors locked.", blocksWritten, sectorsLocked)
	if !writeSuccess {
		msg = fmt.Sprintf("Write completed with errors. %d blocks written (failed sectors: %v), %d/15 sectors locked.", blocksWritten, failedSectors, sectorsLocked)
	}

	resp := models.Response{
		Success: writeSuccess,
		Message: msg,
		Data: models.CardWriteEncodedLockedResponse{
			SNRHex:        fmt.Sprintf("0x%08X", snr),
			SNRDecimal:    snr,
			BrancaToken:   token,
			BytesWritten:  blocksWritten * 16,
			BlocksWritten: blocksWritten,
			SectorsLocked: sectorsLocked,
			Locked:        locked,
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
		"success":   false,
		"error":     title,
		"message":   message,
		"code":      statusCode,
		"retryable": retryable,
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
		"success":       false,
		"error":         diagErr.Message,
		"error_code":    diagErr.Code,
		"function":      diagErr.Function,
		"suggestion":    diagErr.Suggestion,
		"details":       diagErr.Details,
		"card_type":     diagErr.CardType,
		"retryable":     diagErr.Retryable,
		"documentation": diagErr.Documentation,
		"code":          statusCode,
	})
}
