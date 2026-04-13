package models

// CardDetectRequest is the request for detecting a card
type CardDetectRequest struct {
	Mode int `json:"mode"` // 0 = IDLE, 1 = ALL
}

// CardReadRequest is the request for reading a card block
type CardReadRequest struct {
	Mode     int    `json:"mode"`           // 0 = IDLE, 1 = ALL
	Sector   int    `json:"sector"`         // 0-15
	Block    int    `json:"block"`          // 0-63
	KeyMode  int    `json:"key_mode"`       // 0-2 for KEY A, 4-6 for KEY B
	Key      string `json:"key"`            // Hex string (12 chars for 6 bytes)
	UsePass  bool   `json:"use_pass"`       // If true, use dc_authentication_pass instead of load_key+auth
}

// CardWriteRequest is the request for writing a card block
type CardWriteRequest struct {
	Mode    int    `json:"mode"`           // 0 = IDLE, 1 = ALL
	Sector  int    `json:"sector"`         // 0-15
	Block   int    `json:"block"`          // 0-63
	KeyMode int    `json:"key_mode"`       // 0-2 for KEY A, 4-6 for KEY B
	Key     string `json:"key"`            // Hex string (12 chars for 6 bytes)
	Data    string `json:"data"`           // Hex string (32 chars for 16 bytes)
	UsePass bool   `json:"use_pass"`       // If true, use dc_authentication_pass
}

// CardHaltRequest is the request for halting a card
type CardHaltRequest struct{}

// DeviceBeepRequest is the request for making a beep sound
type DeviceBeepRequest struct {
	Ms int `json:"ms"` // Duration in milliseconds
}

// DeviceResetRequest is the request for RF reset
type DeviceResetRequest struct {
	Ms int `json:"ms"` // Reset duration in milliseconds
}

// EEPROMReadRequest is the request for reading EEPROM
type EEPROMReadRequest struct {
	Offset int `json:"offset"` // 0-383
	Length int `json:"length"` // 1-384
}

// EEPROMWriteRequest is the request for writing EEPROM
type EEPROMWriteRequest struct {
	Offset int    `json:"offset"` // 0-383
	Data   string `json:"data"`   // Hex string
}

// InitValRequest is the request for initializing a value block
type InitValRequest struct {
	Block int    `json:"block"` // Block address
	Value uint32 `json:"value"` // Initial value
}

// IncrementRequest is the request for incrementing a value block
type IncrementRequest struct {
	Block int    `json:"block"` // Block address
	Value uint32 `json:"value"` // Value to add
}

// DecrementRequest is the request for decrementing a value block
type DecrementRequest struct {
	Block int    `json:"block"` // Block address
	Value uint32 `json:"value"` // Value to subtract
}

// ReadValRequest is the request for reading a value block
type ReadValRequest struct {
	Block int `json:"block"` // Block address
}

// CardReadAllRequest is the request for reading all blocks from the card
type CardReadAllRequest struct {
	Mode    int    `json:"mode"`           // 0 = IDLE, 1 = ALL
	KeyMode int    `json:"key_mode"`       // 0-2 for KEY A, 4-6 for KEY B
	Key     string `json:"key"`            // Hex string (12 chars for 6 bytes)
	UsePass bool   `json:"use_pass"`       // If true, use dc_authentication_pass
}
