package reader

import "fmt"

// ErrorCode maps DLL return codes to meaningful errors
type ErrorCode int

// Common error codes from D8/T8 DLL
const (
	SUCCESS                    ErrorCode = 0
	NO_CARD_DETECTED           ErrorCode = 1
	CARD_NOT_SELECTED          ErrorCode = 2
	AUTHENTICATION_FAILED      ErrorCode = 3
	READ_FAILED                ErrorCode = 4
	WRITE_FAILED               ErrorCode = 5
	INVALID_PARAMETER          ErrorCode = 6
	COMMUNICATION_ERROR        ErrorCode = 7
	TIMEOUT_ERROR              ErrorCode = 8
	UNSUPPORTED_CARD_TYPE      ErrorCode = 9
	CARD_LOCKED                ErrorCode = 10
	EEPROM_ERROR               ErrorCode = 11
	BUFFER_OVERFLOW            ErrorCode = 12
	UNKNOWN_ERROR              ErrorCode = -1
)

// CardType represents the detected card type
type CardType int

const (
	CARD_TYPE_UNKNOWN CardType = iota
	CARD_TYPE_MIFARE_CLASSIC_1K
	CARD_TYPE_MIFARE_CLASSIC_4K
	CARD_TYPE_MIFARE_ULTRALIGHT
	CARD_TYPE_MIFARE_DESFIRE
	CARD_TYPE_ISO14443_A
	CARD_TYPE_ISO14443_B
	CARD_TYPE_ISO15693
)

// DiagnosticError provides detailed error information for debugging
type DiagnosticError struct {
	Code          int
	Function      string
	Message       string
	CardType      string
	Suggestion    string
	Details       string
	Retryable     bool
	Documentation string
}

// NewDiagnosticError creates a diagnostic error
func NewDiagnosticError(code int, function string, cardType string) *DiagnosticError {
	err := &DiagnosticError{
		Code:      code,
		Function:  function,
		CardType:  cardType,
		Retryable: true,
	}

	// Decode error code and provide suggestions
	err.decodeDLLError()

	return err
}

// decodeDLLError maps DLL error codes to human-readable messages
func (e *DiagnosticError) decodeDLLError() {
	switch e.Code {
	case 0:
		e.Message = "Operation successful"
		e.Retryable = false

	case 1, -1468203008: // Most common "no card" codes
		e.Message = "No card detected"
		e.Suggestion = "Place a compatible Mifare card on the reader"
		e.Details = "The reader's RF field is active but no valid card is in range. " +
			"Ensure card is compatible (Mifare Classic 1K/4K recommended for testing)."
		e.Documentation = "See TROUBLESHOOTING.md → Issue 1: dc_card returned -1468203008"

	case 2:
		e.Message = "Card not selected"
		e.Suggestion = "Call /card/detect first before read/write operations"
		e.Details = "Authentication or read/write attempted without card selection. " +
			"Card detection must precede all card operations."
		e.Documentation = "See API_SPECIFICATION.md → Typical Workflow"

	case 3:
		e.Message = "Authentication failed"
		e.Suggestion = "Verify the card key is correct (default: FFFFFFFFFFFF). Try key_mode 0 or 4."
		e.Details = "Card authentication failed. This typically means:\n" +
			"1. Wrong key for the card\n" +
			"2. Wrong key_mode (0-2 for KEY A, 4-6 for KEY B)\n" +
			"3. Card sector access bits prevent authentication"
		e.Documentation = "See TROUBLESHOOTING.md → Issue 2: Authentication failed"

	case 4:
		e.Message = "Block read failed"
		e.Suggestion = "Verify sector/block range (0-15 sectors, 0-3 blocks per sector) and re-authenticate"
		e.Details = "Read operation failed after authentication. " +
			"Check if the block address is valid and accessible."
		e.Documentation = "See API_SPECIFICATION.md → Card Read endpoint"

	case 5:
		e.Message = "Block write failed"
		e.Suggestion = "Ensure block is not read-only (avoid block 3 - sector trailer). Verify write key permission."
		e.Details = "Write operation failed. Block 3 of each sector (trailer) is usually read-only. " +
			"Also check if card has write protection on this block."
		e.Documentation = "See TROUBLESHOOTING.md → Issue 3: Write failed"

	case 6:
		e.Message = "Invalid parameter"
		e.Suggestion = "Check parameter ranges: sector (0-15), block (0-63), key (12 hex chars)"
		e.Details = "API received invalid parameters. Verify all inputs match expected format and ranges."
		e.Retryable = false

	case 7:
		e.Message = "Communication error with device"
		e.Suggestion = "Check USB connection, restart server, verify DLL_NAME environment variable"
		e.Details = "Serial communication with the D8/T8 device failed. " +
			"Device may be disconnected or unresponsive."
		e.Documentation = "See TROUBLESHOOTING.md → Issue 4: DLL/Device issues"

	case 8:
		e.Message = "Device timeout"
		e.Suggestion = "Device not responding. Try /device/reset, check USB connection, restart server"
		e.Details = "Device operation timed out waiting for response."

	case 9:
		e.Message = "Unsupported card type"
		e.CardType = "Unknown/Unsupported"
		e.Suggestion = "Use a compatible card: Mifare Classic 1K/4K (recommended), Mifare Ultralight, ISO14443"
		e.Details = "The reader detected a card but it is not one of the supported types. " +
			"For best compatibility, use a Mifare Classic 1K or 4K card."
		e.Documentation = "See API_SPECIFICATION.md → Card Types Supported"

	case 10:
		e.Message = "Card is locked/write protected"
		e.Suggestion = "Card has hardware write protection. Try reading instead of writing."
		e.Details = "The card or specific blocks are write-protected and cannot be modified."
		e.Retryable = false

	case 11:
		e.Message = "EEPROM operation failed"
		e.Suggestion = "Verify offset and length parameters (0-383 total). Check device connection."
		e.Details = "Device EEPROM read/write failed. Ensure parameters are within bounds."

	default:
		e.Message = fmt.Sprintf("Device error code %d", e.Code)
		e.Suggestion = "Check device logs, try /device/reset, verify USB connection"
		e.Details = fmt.Sprintf("Received unexpected error code %d from %s. " +
			"This may indicate a device issue or communication problem.", e.Code, e.Function)
	}
}

// Error returns a formatted error string
func (e *DiagnosticError) Error() string {
	return fmt.Sprintf("%s (Code: %d, Function: %s)", e.Message, e.Code, e.Function)
}

// FullDiagnostic returns a detailed diagnostic string
func (e *DiagnosticError) FullDiagnostic() string {
	diagnostic := fmt.Sprintf(`
ERROR DIAGNOSIS
═══════════════════════════════════════
Function: %s
Code: %d
Message: %s
`, e.Function, e.Code, e.Message)

	if e.CardType != "" {
		diagnostic += fmt.Sprintf(`Card Type: %s
`, e.CardType)
	}

	diagnostic += fmt.Sprintf(`
WHAT TO DO:
───────────────────────────────────────
%s

DETAILS:
───────────────────────────────────────
%s

Retryable: %v
`, e.Suggestion, e.Details, e.Retryable)

	if e.Documentation != "" {
		diagnostic += fmt.Sprintf(`
DOCUMENTATION:
───────────────────────────────────────
%s
`, e.Documentation)
	}

	return diagnostic
}

// CardTypeString returns the human-readable card type
func CardTypeString(cardType CardType) string {
	switch cardType {
	case CARD_TYPE_MIFARE_CLASSIC_1K:
		return "Mifare Classic 1K (Supported - Recommended)"
	case CARD_TYPE_MIFARE_CLASSIC_4K:
		return "Mifare Classic 4K (Supported)"
	case CARD_TYPE_MIFARE_ULTRALIGHT:
		return "Mifare Ultralight (Supported)"
	case CARD_TYPE_MIFARE_DESFIRE:
		return "Mifare DESFire (Supported)"
	case CARD_TYPE_ISO14443_A:
		return "ISO14443 Type A (Supported)"
	case CARD_TYPE_ISO14443_B:
		return "ISO14443 Type B (Supported)"
	case CARD_TYPE_ISO15693:
		return "ISO15693 (Supported)"
	case CARD_TYPE_UNKNOWN:
		return "Unknown Card Type (Unsupported)"
	default:
		return "Unknown"
	}
}

// DetectCardType attempts to identify the card type from SNR
// This is a heuristic based on card structure
func DetectCardType(snr uint32) CardType {
	// In a real implementation, you would:
	// 1. Try to read block 0 (manufacturer data)
	// 2. Check ATQA response from dc_request
	// 3. Analyze SAK byte
	// For now, return unknown - would need enhanced protocol support
	return CARD_TYPE_UNKNOWN
}
