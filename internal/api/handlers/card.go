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
//	sector 0  (blocks 0-3)  â€“ UID/MFG + MAD (contains false 0x03 bytes)
//	sector 1+ (blocks 4-63) â€“ CC (E1 10 ...) followed by NDEF TLV: 03 [len] [records]
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
	ndefPayload = append(ndefPayload, byte(len(lang))) // status byte: UTF-8, langLen=2 â†’ 0x02
	ndefPayload = append(ndefPayload, []byte(lang)...)
	ndefPayload = append(ndefPayload, []byte(text)...)

	// NDEF record: header depends on SR (short record) flag.
	// SR=1 when payload length â‰¤ 255, payload length is 1 byte (header 0xD1).
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

// requireReader writes a 503 and returns false when no reader is connected.
// Use at the top of every handler that calls the physical reader.
func (h *CardHandlers) requireReader(w http.ResponseWriter) bool {
	if h.Reader == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(models.Response{
			Success: false,
			Message: "Card reader not available. Connect the device and restart the server.",
			Code:    http.StatusServiceUnavailable,
		})
		return false
	}
	return true
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
	if !h.requireReader(w) {
		return
	}
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

// Identify godoc
//
//	@Summary		Identify NFC card type
//	@Description	Detects a card and returns its ATQA, SAK, and decoded card type (e.g. MIFARE Classic 1K)
//	@Tags			Card Operations
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardDetectRequest						true	"Detect request"
//	@Success		200		{object}	models.Response{data=models.CardIdentifyResponse}
//	@Failure		404		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/identify [post]
func (h *CardHandlers) Identify(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/identify")
	if !h.requireReader(w) {
		return
	}
	var req models.CardDetectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}

	info, err := h.Reader.DetectCardInfo(req.Mode)
	if err != nil {
		diagErr := parseDLLError(err.Error(), "dc_request")
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no card detected") {
			statusCode = http.StatusNotFound
			diagErr.Suggestion = "No card detected. Place card on reader."
		}
		respondWithDiagnosticError(w, diagErr, statusCode)
		return
	}
	h.Reader.Halt()

	cardType, writable := decodeCardType(info.SAK, info.ATQA)
	log.Printf("[API] Identify: SNR=%08X ATQA=0x%04X SAK=0x%02X → %s", info.SNR, info.ATQA, info.SAK, cardType)

	resp := models.Response{
		Success: true,
		Message: "Card identified",
		Data: models.CardIdentifyResponse{
			SNRHex:     fmt.Sprintf("0x%08X", info.SNR),
			SNRDecimal: info.SNR,
			ATQA:       fmt.Sprintf("0x%04X", info.ATQA),
			SAK:        fmt.Sprintf("0x%02X", info.SAK),
			CardType:   cardType,
			Writable:   writable,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// decodeCardType returns a human-readable card type name and whether the card
// is writable by the MIFARE Classic write commands used in this application.
func decodeCardType(sak uint8, atqa uint16) (string, bool) {
	switch sak {
	case 0x08:
		return "MIFARE Classic 1K", true
	case 0x18:
		return "MIFARE Classic 4K", true
	case 0x09:
		return "MIFARE Classic Mini", true
	case 0x00:
		if atqa == 0x0044 {
			return "MIFARE Ultralight / NTAG (not supported by this API)", false
		}
		return fmt.Sprintf("Unknown SAK=0x00 ATQA=0x%04X", atqa), false
	case 0x20:
		return "MIFARE DESFire / MIFARE Plus SL3 (not supported by this API)", false
	case 0x28:
		return "SmartMX with MIFARE Classic 1K emulation", true
	case 0x38:
		return "SmartMX with MIFARE Classic 4K emulation", true
	default:
		return fmt.Sprintf("Unknown (SAK=0x%02X ATQA=0x%04X)", sak, atqa), false
	}
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
	if !h.requireReader(w) {
		return
	}
	if err := h.Reader.Halt(); err != nil {
		log.Printf("[API] ERROR: Halt failed: %v", err)
		respondError(w, "Halt failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Message: "Card halted (deselected). Workflow: DETECT â†’ [READ/WRITE] â†’ HALT â†’ [REPEAT]. Next: call /card/detect to select another card or perform more operations.",
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
	if !h.requireReader(w) {
		return
	}
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
	log.Printf("[API] ReadDecoded: SNR decimal=%d â†’ branca key=%s", snr, brancaKey)

	// Step 3: Read all 64 blocks using a sector-based loop.
	// For each sector, try the user-provided key first, then well-known NDEF keys:
	//   D3F7D3F7D3F7 â€” NFC Forum NDEF data sectors (1-15, written by Android apps)
	//   A0A1A2A3A4A5 â€” NFC Forum MAD / sector 0 key
	//   FFFFFFFFFFFF â€” factory default
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
			log.Printf("[API] ReadDecoded: sector %d key[%d]=%X â€” LoadKey+Auth", sector, keyIdx, tryKey)

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

			// Confirm authentication actually worked â€” a wrong key can still return 0 from Authenticate.
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
		// Sectors 1-15 skip the sector trailer (block 3) â€” it holds access keys/bits, not NDEF data,
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
	if !h.requireReader(w) {
		return
	}
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

	// Detect card â†’ get SNR
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
	log.Printf("[API] WriteEncoded: SNR decimal=%d â†’ branca key=%s", snr, brancaKey)

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

	// Sectors 1-15: 3 data blocks each = 45 * 16 = 720 bytes total NDEF area.
	// NDEF TLV is written starting at block 4 (sector 1, block 0), matching NFC Tools layout.
	const maxNDEFBytes = 720
	if len(ndefTLV) > maxNDEFBytes {
		respondWithError(w, "Data too large",
			fmt.Sprintf("NDEF payload is %d bytes; card capacity is %d bytes. Reduce the JSON payload size.", len(ndefTLV), maxNDEFBytes),
			http.StatusBadRequest, false)
		return
	}

	// Pad NDEF TLV to fill all 720 bytes (zero-fill remaining blocks)
	ndefPadded := make([]byte, maxNDEFBytes)
	copy(ndefPadded, ndefTLV)

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
			log.Printf("[API] WriteEncoded: sector %d key[%d] â€” LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] WriteEncoded: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] WriteEncoded: sector %d Auth failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			// Verify auth is genuine — dc_authentication can false-positive for FFFFFFFFFFFF
			if _, readErr := h.Reader.ReadBlock(block0); readErr != nil {
				log.Printf("[API] WriteEncoded: sector %d auth false-positive (%v), re-detecting", sector, readErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] WriteEncoded: sector %d authenticated + verified with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] WriteEncoded: sector %d all keys failed, skipping", sector)
			failedSectors = append(failedSectors, sector)
			ndefOffset += 3 * 16
			continue
		}

		// Write NDEF data to blocks 0, 1, 2 of each sector (skip trailer block 3).
		// Sector 1 block 0 (block 4) holds the start of the NDEF TLV, matching NFC Tools layout.
		for blockInSector := 0; blockInSector < 3; blockInSector++ {
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
	if !h.requireReader(w) {
		return
	}
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
	log.Printf("[API] ReadDecodedSHA: SNR decimal=%d â†’ branca key derived via SHA256+salt", snr)

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
			log.Printf("[API] ReadDecodedSHA: sector %d key[%d]=%X â€” LoadKey+Auth", sector, keyIdx, tryKey)

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
	if !h.requireReader(w) {
		return
	}
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

	// Detect card â†’ get SNR
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
	log.Printf("[API] WriteEncodedSHA: SNR decimal=%d â†’ branca key derived via SHA256+salt", snr)

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

	const maxNDEFBytes = 720
	if len(ndefTLV) > maxNDEFBytes {
		respondWithError(w, "Data too large",
			fmt.Sprintf("NDEF payload is %d bytes; card capacity is %d bytes. Reduce the JSON payload size.", len(ndefTLV), maxNDEFBytes),
			http.StatusBadRequest, false)
		return
	}

	ndefPadded := make([]byte, maxNDEFBytes)
	copy(ndefPadded, ndefTLV)

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
			log.Printf("[API] WriteEncodedSHA: sector %d key[%d] â€” LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				log.Printf("[API] WriteEncodedSHA: sector %d LoadKey failed: %v", sector, loadErr)
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				log.Printf("[API] WriteEncodedSHA: sector %d Auth failed, re-detecting: %v", sector, authErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			// Verify auth is genuine — dc_authentication can false-positive for FFFFFFFFFFFF
			if _, readErr := h.Reader.ReadBlock(block0); readErr != nil {
				log.Printf("[API] WriteEncodedSHA: sector %d auth false-positive (%v), re-detecting", sector, readErr)
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] WriteEncodedSHA: sector %d authenticated + verified with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] WriteEncodedSHA: sector %d all keys failed, skipping", sector)
			failedSectors = append(failedSectors, sector)
			ndefOffset += 3 * 16
			continue
		}

		for blockInSector := 0; blockInSector < 3; blockInSector++ {
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
//	bytes 10-15: newKey (KEY B â€” also replaced so KEY B = passkey-derived)
//
// Authentication is tried in two rounds per sector:
//  1. KEY B mode (4) â€” works on standard NDEF cards where only KEY B may write the trailer
//  2. KEY A mode (userKeyMode) â€” works on factory-fresh cards where KEY A can write the trailer
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

	// Access bytes for sectors 1-15: 0x7F 0x07 0x88 (C1=0,C2=1,C3=1 for trailer,
	// C1=C2=C3=0 for data blocks) + 0x40 user/GPB byte.
	// These are the NDEF standard access conditions.
	const (
		acB0 byte = 0x7F // trailer byte 6
		acB1 byte = 0x07 // trailer byte 7
		acB2 byte = 0x88 // trailer byte 8
		acB3 byte = 0x40 // trailer byte 9 (user / GPB)
	)

	locked := 0
	for sector := 1; sector < 16; sector++ {
		sectorLocked := false

		trailerAddr := sector*4 + 3
		var trailerBlock [16]byte
		copy(trailerBlock[0:6], newKey[:])
		trailerBlock[6] = acB0
		trailerBlock[7] = acB1
		trailerBlock[8] = acB2
		trailerBlock[9] = acB3
		copy(trailerBlock[10:16], newKey[:])

		tryLock := func(keyMode int, modeLabel string) bool {
			for _, tryKey := range lockKeys {
				if loadErr := h.Reader.LoadKey(keyMode, sector, tryKey); loadErr != nil {
					log.Printf("[LOCK] sector %d %s LoadKey failed: %v", sector, modeLabel, loadErr)
					continue
				}
				if authErr := h.Reader.Authenticate(keyMode, sector); authErr != nil {
					log.Printf("[LOCK] sector %d %s auth failed (%v), re-detecting", sector, modeLabel, authErr)
					h.Reader.DetectCard(mode) //nolint:errcheck
					continue
				}
				// Try dc_changeb3 first (the DLL's dedicated trailer-update function).
				cbErr := h.Reader.ChangeBlock3(sector, newKey, acB0, acB1, acB2, acB3, 0, newKey)
				if cbErr == nil {
					log.Printf("[LOCK] sector %d locked via %s (dc_changeb3)", sector, modeLabel)
					return true
				}
				log.Printf("[LOCK] sector %d dc_changeb3 failed (%v), falling back to dc_write", sector, cbErr)
				// Fallback: raw dc_write on the trailer block. Works on factory-default
				// cards where KEY A authentication allows direct trailer writes.
				h.Reader.DetectCard(mode) //nolint:errcheck
				if loadErr2 := h.Reader.LoadKey(keyMode, sector, tryKey); loadErr2 == nil {
					if authErr2 := h.Reader.Authenticate(keyMode, sector); authErr2 == nil {
						if writeErr := h.Reader.WriteBlock(trailerAddr, trailerBlock); writeErr == nil {
							log.Printf("[LOCK] sector %d locked via %s (dc_write fallback)", sector, modeLabel)
							return true
						} else {
							log.Printf("[LOCK] sector %d dc_write fallback also failed: %v â€” re-detecting", sector, writeErr)
							h.Reader.DetectCard(mode) //nolint:errcheck
						}
					}
				}
				continue
			}
			return false
		}

		// Round 1: KEY B (mode 4) â€” required on NDEF-formatted cards where access bits
		// only allow KEY B to write the sector trailer.
		if tryLock(4, "KEY-B") {
			sectorLocked = true
		}

		// Round 2: KEY A (userKeyMode) â€” works on factory-fresh cards where KEY A
		// (mode 0/1/2) can write the sector trailer.
		if !sectorLocked && tryLock(userKeyMode, "KEY-A") {
			sectorLocked = true
		}

		if sectorLocked {
			locked++
		} else {
			log.Printf("[LOCK] sector %d could not be locked (all keys exhausted in both rounds)", sector)
		}
	}
	return locked
}

// unlockSectorTrailers resets all 15 data sector keys (1-15) back to the factory default (FFFFFFFFFFFF).
// It authenticates using currentKey (the passkey-derived key) and then tries NDEF defaults as fallback.
// Returns the number of sectors successfully unlocked (max 15).
func unlockSectorTrailers(h *CardHandlers, mode int, currentKey [6]byte) int {
	defaultKey, _ := reader.KeyFromHex("FFFFFFFFFFFF")
	ndefDefaults := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	tryKeys := [][6]byte{currentKey}
	for _, kh := range ndefDefaults {
		k, _ := reader.KeyFromHex(kh)
		tryKeys = append(tryKeys, k)
	}

	// Standard NDEF access conditions (same as lockSectorTrailers)
	const (
		acB0 byte = 0x7F
		acB1 byte = 0x07
		acB2 byte = 0x88
		acB3 byte = 0x40
	)

	unlocked := 0
	for sector := 1; sector < 16; sector++ {
		sectorUnlocked := false

		trailerAddr := sector*4 + 3
		var trailerBlock [16]byte
		copy(trailerBlock[0:6], defaultKey[:])
		trailerBlock[6] = acB0
		trailerBlock[7] = acB1
		trailerBlock[8] = acB2
		trailerBlock[9] = acB3
		copy(trailerBlock[10:16], defaultKey[:])

		tryUnlock := func(keyMode int, modeLabel string) bool {
			for _, tryKey := range tryKeys {
				if loadErr := h.Reader.LoadKey(keyMode, sector, tryKey); loadErr != nil {
					log.Printf("[UNLOCK] sector %d %s LoadKey failed: %v", sector, modeLabel, loadErr)
					continue
				}
				if authErr := h.Reader.Authenticate(keyMode, sector); authErr != nil {
					log.Printf("[UNLOCK] sector %d %s auth failed (%v), re-detecting", sector, modeLabel, authErr)
					h.Reader.DetectCard(mode) //nolint:errcheck
					continue
				}
				cbErr := h.Reader.ChangeBlock3(sector, defaultKey, acB0, acB1, acB2, acB3, 0, defaultKey)
				if cbErr == nil {
					log.Printf("[UNLOCK] sector %d unlocked via %s (dc_changeb3)", sector, modeLabel)
					return true
				}
				log.Printf("[UNLOCK] sector %d dc_changeb3 failed (%v), falling back to dc_write", sector, cbErr)
				h.Reader.DetectCard(mode) //nolint:errcheck
				if loadErr2 := h.Reader.LoadKey(keyMode, sector, tryKey); loadErr2 == nil {
					if authErr2 := h.Reader.Authenticate(keyMode, sector); authErr2 == nil {
						if writeErr := h.Reader.WriteBlock(trailerAddr, trailerBlock); writeErr == nil {
							log.Printf("[UNLOCK] sector %d unlocked via %s (dc_write fallback)", sector, modeLabel)
							return true
						} else {
							log.Printf("[UNLOCK] sector %d dc_write fallback also failed: %v â€” re-detecting", sector, writeErr)
							h.Reader.DetectCard(mode) //nolint:errcheck
						}
					}
				}
			}
			return false
		}

		if tryUnlock(4, "KEY-B") {
			sectorUnlocked = true
		}
		if !sectorUnlocked && tryUnlock(0, "KEY-A") {
			sectorUnlocked = true
		}

		if sectorUnlocked {
			unlocked++
		} else {
			log.Printf("[UNLOCK] sector %d could not be unlocked (all keys exhausted)", sector)
		}
	}
	return unlocked
}

// SetPassword godoc
//
//	@Summary		Set write-protection password
//	@Description	Changes all 15 sector keys to a key derived from the passkey (MD5(passkey)[0:6]). Card data is not modified. After this, writing to the card requires authenticating with the passkey. Use remove-password to restore the card to the default key.
//	@Tags			Card Operations
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardSetPasswordRequest				true	"Set password request"
//	@Success		200		{object}	models.Response{data=models.CardSetPasswordResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/set-password [post]
func (h *CardHandlers) SetPassword(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/set-password")
	if !h.requireReader(w) {
		return
	}
	var req models.CardSetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if strings.TrimSpace(req.Passkey) == "" {
		respondError(w, "passkey is required", http.StatusBadRequest)
		return
	}

	key, err := reader.KeyFromHex(req.Key)
	if err != nil {
		respondError(w, "Invalid key: "+err.Error(), http.StatusBadRequest)
		return
	}

	passkeyMifareKey, passkeyKeyHex := passkeyToMifareKey(req.Passkey)
	log.Printf("[API] SetPassword: passkey â†’ Mifare key %s", passkeyKeyHex)

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
	log.Printf("[API] SetPassword: card detected SNR=%08X (%d)", snr, snr)

	sectorsLocked := lockSectorTrailers(h, req.Mode, req.KeyMode, key, passkeyMifareKey)
	log.Printf("[API] SetPassword: %d/15 sectors locked", sectorsLocked)

	resp := models.Response{
		Success: true,
		Message: fmt.Sprintf("Password set. %d/15 sectors write-protected.", sectorsLocked),
		Data: models.CardSetPasswordResponse{
			SNRHex:        fmt.Sprintf("0x%08X", snr),
			SNRDecimal:    snr,
			SectorsLocked: sectorsLocked,
			Locked:        sectorsLocked == 15,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// RemovePassword godoc
//
//	@Summary		Remove write-protection password
//	@Description	Resets all 15 sector keys back to the factory default (FFFFFFFFFFFF) by authenticating with the passkey-derived key. After this, the card can be written to without a password.
//	@Tags			Card Operations
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardRemovePasswordRequest				true	"Remove password request"
//	@Success		200		{object}	models.Response{data=models.CardRemovePasswordResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/remove-password [post]
func (h *CardHandlers) RemovePassword(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/remove-password")
	if !h.requireReader(w) {
		return
	}
	var req models.CardRemovePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, "Invalid request body", "Invalid JSON format", http.StatusBadRequest, false)
		return
	}
	if strings.TrimSpace(req.Passkey) == "" {
		respondError(w, "passkey is required", http.StatusBadRequest)
		return
	}

	passkeyMifareKey, passkeyKeyHex := passkeyToMifareKey(req.Passkey)
	log.Printf("[API] RemovePassword: passkey â†’ Mifare key %s", passkeyKeyHex)

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
	log.Printf("[API] RemovePassword: card detected SNR=%08X (%d)", snr, snr)

	sectorsUnlocked := unlockSectorTrailers(h, req.Mode, passkeyMifareKey)
	log.Printf("[API] RemovePassword: %d/15 sectors unlocked", sectorsUnlocked)

	resp := models.Response{
		Success: true,
		Message: fmt.Sprintf("Password removed. %d/15 sectors restored to default key.", sectorsUnlocked),
		Data: models.CardRemovePasswordResponse{
			SNRHex:          fmt.Sprintf("0x%08X", snr),
			SNRDecimal:      snr,
			SectorsUnlocked: sectorsUnlocked,
			Unlocked:        sectorsUnlocked == 15,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ReadNDEF godoc
//
//	@Summary		Read raw NDEF text from card
//	@Description	Reads all card blocks and returns the raw NDEF text record content without any decryption. Works regardless of whether the card was written with MD5 or SHA256 encoding.
//	@Tags			Card Operations
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardReadDecodedRequest					true	"Read request"
//	@Success		200		{object}	models.Response{data=models.CardReadNDEFResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		422		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/read-ndef [post]
func (h *CardHandlers) ReadNDEF(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/read-ndef")
	if !h.requireReader(w) {
		return
	}
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

	log.Printf("[API] ReadNDEF: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] ReadNDEF: card detected SNR=%08X (%d)", snr, snr)

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
			log.Printf("[API] ReadNDEF: sector %d key[%d] — LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			data, readErr := h.Reader.ReadBlock(block0)
			if readErr != nil {
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			block0Data = data
			log.Printf("[API] ReadNDEF: sector %d authenticated + verified with key[%d]", sector, keyIdx)
			break
		}

		blocksToRead := 4
		if sector > 0 {
			blocksToRead = 3
		}

		if !authenticated {
			rawBytes = append(rawBytes, make([]byte, blocksToRead*16)...)
			continue
		}

		rawBytes = append(rawBytes, block0Data[:]...)
		for blockInSector := 1; blockInSector < blocksToRead; blockInSector++ {
			data, readErr := h.Reader.ReadBlock(block0 + blockInSector)
			if readErr != nil {
				rawBytes = append(rawBytes, make([]byte, 16)...)
			} else {
				rawBytes = append(rawBytes, data[:]...)
			}
		}
	}
	h.Reader.Halt()

	text := extractNDEFText(rawBytes)
	if text == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(models.Response{
			Success: false,
			Message: "No NDEF text record found on card",
		})
		return
	}
	log.Printf("[API] ReadNDEF: extracted text len=%d", len(text))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.Response{
		Success: true,
		Message: "NDEF text read successfully",
		Data: models.CardReadNDEFResponse{
			SNRHex:     fmt.Sprintf("0x%08X", snr),
			SNRDecimal: snr,
			Text:       text,
		},
	})
}

// FormatCard godoc
//
//	@Summary		Format (erase) card NDEF data
//	@Description	Erases all NDEF data by writing zeros to data blocks in sectors 1-15. Sector trailers (keys/access bits) are not changed. Equivalent to NFC Tools' format function.
//	@Tags			Card Operations
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.CardReadDecodedRequest					true	"Format request"
//	@Success		200		{object}	models.Response{data=models.CardFormatResponse}
//	@Failure		400		{object}	models.DetailedErrorResponse
//	@Failure		500		{object}	models.DetailedErrorResponse
//	@Router			/api/v1/card/format [post]
func (h *CardHandlers) FormatCard(w http.ResponseWriter, r *http.Request) {
	log.Println("[API] POST /api/v1/card/format")
	if !h.requireReader(w) {
		return
	}
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

	log.Printf("[API] FormatCard: detecting card (mode=%d)...", req.Mode)
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
	log.Printf("[API] FormatCard: card detected SNR=%08X (%d)", snr, snr)

	ndefKeyHexes := []string{"D3F7D3F7D3F7", "A0A1A2A3A4A5", "FFFFFFFFFFFF"}
	allKeys := [][6]byte{key}
	for _, kh := range ndefKeyHexes {
		k, _ := reader.KeyFromHex(kh)
		allKeys = append(allKeys, k)
	}

	var zeroBlock [16]byte
	blocksErased := 0
	failedSectors := []int{}

	for sector := 1; sector < 16; sector++ {
		block0 := sector * 4

		authenticated := false
		for keyIdx, tryKey := range allKeys {
			log.Printf("[API] FormatCard: sector %d key[%d] — LoadKey+Auth", sector, keyIdx)
			if loadErr := h.Reader.LoadKey(req.KeyMode, sector, tryKey); loadErr != nil {
				continue
			}
			if authErr := h.Reader.Authenticate(req.KeyMode, sector); authErr != nil {
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			if _, readErr := h.Reader.ReadBlock(block0); readErr != nil {
				h.Reader.DetectCard(req.Mode) //nolint:errcheck
				continue
			}
			authenticated = true
			log.Printf("[API] FormatCard: sector %d authenticated with key[%d]", sector, keyIdx)
			break
		}

		if !authenticated {
			log.Printf("[API] FormatCard: sector %d all keys failed, skipping", sector)
			failedSectors = append(failedSectors, sector)
			continue
		}

		for blockInSector := 0; blockInSector < 3; blockInSector++ {
			blockAddr := block0 + blockInSector
			if writeErr := h.Reader.WriteBlock(blockAddr, zeroBlock); writeErr != nil {
				log.Printf("[API] FormatCard: WriteBlock %d failed: %v", blockAddr, writeErr)
			} else {
				log.Printf("[API] FormatCard: block %d erased", blockAddr)
				blocksErased++
			}
		}
	}

	h.Reader.Halt()

	success := len(failedSectors) == 0
	msg := fmt.Sprintf("Card formatted successfully. %d blocks erased.", blocksErased)
	if !success {
		msg = fmt.Sprintf("Format completed with errors. %d blocks erased, failed sectors: %v", blocksErased, failedSectors)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.Response{
		Success: success,
		Message: msg,
		Data: models.CardFormatResponse{
			SNRHex:        fmt.Sprintf("0x%08X", snr),
			SNRDecimal:    snr,
			BlocksErased:  blocksErased,
			FailedSectors: failedSectors,
		},
	})
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
