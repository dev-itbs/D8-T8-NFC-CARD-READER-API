package models

// Response is a generic response wrapper
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Code    int         `json:"code,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// CardDetectResponse is the response for card detection
type CardDetectResponse struct {
	SNRHex     string `json:"snr_hex"`     // Hex representation like "0x1A2B3C4D"
	SNRDecimal uint32 `json:"snr_decimal"` // Decimal representation
}

// CardReadResponse is the response for reading a card block
type CardReadResponse struct {
	Data      string `json:"data"`       // Hex string of 16 bytes (32 chars)
	DataBytes []byte `json:"data_bytes"` // Raw byte array
}

// CardReadAllResponse is the response for reading all blocks from the card
type CardReadAllResponse struct {
	SNRHex        string            `json:"snr_hex"`        // Card serial number
	SNRDecimal    uint32            `json:"snr_decimal"`    // Card serial number (decimal)
	TotalBytes    int               `json:"total_bytes"`    // Total bytes read (1024 for Mifare 1K)
	FullData      string            `json:"full_data"`      // All 64 blocks concatenated (2048 hex chars)
	FullDataBase64 string           `json:"full_data_base64"` // Base64 encoded full data
	BlockMap      map[string]string `json:"blocks"`         // Individual blocks by number
}

// CardWriteResponse is the response for writing a card block (empty data, just success)
type CardWriteResponse struct{}

// VersionResponse is the response for getting device version
type VersionResponse struct {
	Version string `json:"version"`
}

// EEPROMReadResponse is the response for reading EEPROM
type EEPROMReadResponse struct {
	Data string `json:"data"`       // Hex string
	Bytes []byte `json:"bytes,omitempty"` // Raw byte array
}

// EEPROMWriteResponse is the response for writing EEPROM (empty data, just success)
type EEPROMWriteResponse struct{}

// InitValResponse is the response for initializing a value block
type InitValResponse struct{}

// IncrementResponse is the response for incrementing a value block
type IncrementResponse struct{}

// DecrementResponse is the response for decrementing a value block
type DecrementResponse struct{}

// ReadValResponse is the response for reading a value block
type ReadValResponse struct {
	Value uint32 `json:"value"`
}

// CardDecodeResponse is the response for decoding a Branca token
type CardDecodeResponse struct {
	SNRDecimal  uint32      `json:"snr_decimal"`
	BrancaToken string      `json:"branca_token"`
	Payload     interface{} `json:"payload"` // Decoded JSON from the Branca token
}

// CardReadDecodedResponse combines ReadAll + Branca decode in one response
type CardReadDecodedResponse struct {
	SNRHex      string      `json:"snr_hex"`
	SNRDecimal  uint32      `json:"snr_decimal"`
	BrancaToken string      `json:"branca_token"`
	Payload     interface{} `json:"payload"` // Decoded JSON from the Branca token
}

// CardWriteEncodedResponse is the response for encoding JSON + writing to card
type CardWriteEncodedResponse struct {
	SNRHex        string `json:"snr_hex"`
	SNRDecimal    uint32 `json:"snr_decimal"`
	BrancaToken   string `json:"branca_token"`
	BytesWritten  int    `json:"bytes_written"`
	BlocksWritten int    `json:"blocks_written"`
}

// CardSetPasswordResponse is returned after setting a write-protection password on the card.
type CardSetPasswordResponse struct {
	SNRHex        string `json:"snr_hex"`
	SNRDecimal    uint32 `json:"snr_decimal"`
	SectorsLocked int    `json:"sectors_locked"` // number of sectors whose keys were changed (max 15)
	Locked        bool   `json:"locked"`          // true when all 15 sectors were locked
}

// CardFormatResponse is returned after erasing NDEF data from the card.
type CardFormatResponse struct {
	SNRHex        string `json:"snr_hex"`
	SNRDecimal    uint32 `json:"snr_decimal"`
	BlocksErased  int    `json:"blocks_erased"`
	FailedSectors []int  `json:"failed_sectors,omitempty"`
}

// CardReadNDEFResponse is returned after reading raw NDEF text from the card.
type CardReadNDEFResponse struct {
	SNRHex     string `json:"snr_hex"`
	SNRDecimal uint32 `json:"snr_decimal"`
	Text       string `json:"text"`
}

// CardRemovePasswordResponse is returned after removing the write-protection password.
type CardRemovePasswordResponse struct {
	SNRHex          string `json:"snr_hex"`
	SNRDecimal      uint32 `json:"snr_decimal"`
	SectorsUnlocked int    `json:"sectors_unlocked"` // number of sectors restored to default key
	Unlocked        bool   `json:"unlocked"`          // true when all 15 sectors were restored
}

// CardIdentifyResponse is returned by the identify endpoint
type CardIdentifyResponse struct {
	SNRHex     string `json:"snr_hex"`
	SNRDecimal uint32 `json:"snr_decimal"`
	ATQA       string `json:"atqa"`        // e.g. "0x0004"
	SAK        string `json:"sak"`         // e.g. "0x08"
	CardType   string `json:"card_type"`   // e.g. "MIFARE Classic 1K"
	Writable   bool   `json:"writable"`    // true for Classic 1K/4K/Mini
}

// ErrorResponse is returned on error (wrapped in Response)
type ErrorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

// DetailedErrorResponse provides comprehensive diagnostic information
type DetailedErrorResponse struct {
	Success       bool   `json:"success"`
	Error         string `json:"error"`
	ErrorCode     int    `json:"error_code"`
	Function      string `json:"function"`
	Message       string `json:"message,omitempty"`
	Suggestion    string `json:"suggestion"`
	Details       string `json:"details"`
	CardType      string `json:"card_type,omitempty"`
	Retryable     bool   `json:"retryable"`
	Documentation string `json:"documentation,omitempty"`
	Code          int    `json:"code"`
}
