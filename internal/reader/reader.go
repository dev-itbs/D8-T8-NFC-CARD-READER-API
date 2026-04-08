package reader

import (
	"encoding/hex"
	"fmt"
	"sync"
	"unsafe"
)

// Reader wraps the D8/T8 card reader device
type Reader struct {
	loader *DLLLoader
	icdev  uintptr
	mu     sync.Mutex
}

// New initializes the reader on the specified port and baud rate
func New(dllName string, port, baud int) (*Reader, error) {
	loader, err := NewDLLLoader(dllName)
	if err != nil {
		return nil, err
	}

	// Call dc_init(port, baud)
	ret, err := loader.Call("dc_init", uintptr(port), uintptr(baud))
	if err != nil {
		loader.Close()
		return nil, fmt.Errorf("dc_init failed: %w", err)
	}

	if ret <= 0 {
		loader.Close()
		return nil, fmt.Errorf("dc_init returned %d (expected > 0)", ret)
	}

	return &Reader{
		loader: loader,
		icdev:  uintptr(ret),
	}, nil
}

// Close closes the reader connection
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.icdev == 0 {
		return nil
	}

	ret, err := r.loader.Call("dc_exit", r.icdev)
	if err != nil {
		return fmt.Errorf("dc_exit failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_exit returned %d (expected 0)", ret)
	}

	r.icdev = 0
	r.loader.Close()
	return nil
}

// DetectCard detects a card and returns its serial number
// mode: 0 = IDLE, 1 = ALL
func (r *Reader) DetectCard(mode int) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var snr uint32
	ret, err := r.loader.Call("dc_card", r.icdev, uintptr(mode), uintptr(unsafe.Pointer(&snr)))
	if err != nil {
		return 0, fmt.Errorf("dc_card failed: %w", err)
	}

	if ret != 0 {
		return 0, fmt.Errorf("dc_card returned %d (no card or error)", ret)
	}

	return snr, nil
}

// LoadKey loads an authentication key into the reader's RAM
// mode: 0-2 for KEY A, 4-6 for KEY B
func (r *Reader) LoadKey(mode, sector int, key [6]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_load_key", r.icdev, uintptr(mode), uintptr(sector), uintptr(unsafe.Pointer(&key[0])))
	if err != nil {
		return fmt.Errorf("dc_load_key failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_load_key returned %d", ret)
	}

	return nil
}

// Authenticate authenticates a sector with a pre-loaded key
func (r *Reader) Authenticate(mode, sector int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_authentication", r.icdev, uintptr(mode), uintptr(sector))
	if err != nil {
		return fmt.Errorf("dc_authentication failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_authentication returned %d", ret)
	}

	return nil
}

// AuthenticateWithPass authenticates a block directly with a password (no separate load_key needed)
// mode: 0-2 for KEY A, 4-6 for KEY B
func (r *Reader) AuthenticateWithPass(mode, blockAddr int, key [6]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_authentication_pass", r.icdev, uintptr(mode), uintptr(blockAddr), uintptr(unsafe.Pointer(&key[0])))
	if err != nil {
		return fmt.Errorf("dc_authentication_pass failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_authentication_pass returned %d", ret)
	}

	return nil
}

// ReadBlock reads 16 bytes from a block
func (r *Reader) ReadBlock(addr int) ([16]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var data [16]byte
	ret, err := r.loader.Call("dc_read", r.icdev, uintptr(addr), uintptr(unsafe.Pointer(&data[0])))
	if err != nil {
		return data, fmt.Errorf("dc_read failed: %w", err)
	}

	if ret != 0 {
		return data, fmt.Errorf("dc_read returned %d", ret)
	}

	return data, nil
}

// WriteBlock writes 16 bytes to a block
func (r *Reader) WriteBlock(addr int, data [16]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_write", r.icdev, uintptr(addr), uintptr(unsafe.Pointer(&data[0])))
	if err != nil {
		return fmt.Errorf("dc_write failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_write returned %d", ret)
	}

	return nil
}

// Halt halts the card (must be called after operations)
func (r *Reader) Halt() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_halt", r.icdev)
	if err != nil {
		return fmt.Errorf("dc_halt failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_halt returned %d", ret)
	}

	return nil
}

// Beep triggers the reader's buzzer
func (r *Reader) Beep(ms int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_beep", r.icdev, uintptr(ms))
	if err != nil {
		return fmt.Errorf("dc_beep failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_beep returned %d", ret)
	}

	return nil
}

// GetVersion reads the reader's firmware version
func (r *Reader) GetVersion() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var buffer [5]byte
	ret, err := r.loader.Call("dc_getver", r.icdev, uintptr(unsafe.Pointer(&buffer[0])))
	if err != nil {
		return "", fmt.Errorf("dc_getver failed: %w", err)
	}

	if ret != 0 {
		return "", fmt.Errorf("dc_getver returned %d", ret)
	}

	return fmt.Sprintf("%d.%d.%d", buffer[0], buffer[1], buffer[2]), nil
}

// Reset performs an RF reset on the device
func (r *Reader) Reset(ms int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_reset", r.icdev, uintptr(ms))
	if err != nil {
		return fmt.Errorf("dc_reset failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_reset returned %d", ret)
	}

	return nil
}

// ReadEEPROM reads data from the reader's EEPROM
func (r *Reader) ReadEEPROM(offset, length int) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if length > 384 {
		return nil, fmt.Errorf("length exceeds max EEPROM size (384)")
	}

	buffer := make([]byte, length)
	ret, err := r.loader.Call("dc_srd_eeprom", r.icdev, uintptr(offset), uintptr(length), uintptr(unsafe.Pointer(&buffer[0])))
	if err != nil {
		return nil, fmt.Errorf("dc_srd_eeprom failed: %w", err)
	}

	if ret != 0 {
		return nil, fmt.Errorf("dc_srd_eeprom returned %d", ret)
	}

	return buffer, nil
}

// WriteEEPROM writes data to the reader's EEPROM
func (r *Reader) WriteEEPROM(offset int, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(data) > 384 {
		return fmt.Errorf("data exceeds max EEPROM size (384)")
	}

	ret, err := r.loader.Call("dc_swr_eeprom", r.icdev, uintptr(offset), uintptr(len(data)), uintptr(unsafe.Pointer(&data[0])))
	if err != nil {
		return fmt.Errorf("dc_swr_eeprom failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_swr_eeprom returned %d", ret)
	}

	return nil
}

// InitVal initializes a value block
func (r *Reader) InitVal(addr int, value uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_initval", r.icdev, uintptr(addr), uintptr(value))
	if err != nil {
		return fmt.Errorf("dc_initval failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_initval returned %d", ret)
	}

	return nil
}

// Increment increments a value block
func (r *Reader) Increment(addr int, value uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_increment", r.icdev, uintptr(addr), uintptr(value))
	if err != nil {
		return fmt.Errorf("dc_increment failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_increment returned %d", ret)
	}

	return nil
}

// Decrement decrements a value block
func (r *Reader) Decrement(addr int, value uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret, err := r.loader.Call("dc_decrement", r.icdev, uintptr(addr), uintptr(value))
	if err != nil {
		return fmt.Errorf("dc_decrement failed: %w", err)
	}

	if ret != 0 {
		return fmt.Errorf("dc_decrement returned %d", ret)
	}

	return nil
}

// ReadVal reads a value from a value block
func (r *Reader) ReadVal(addr int) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var value uint32
	ret, err := r.loader.Call("dc_readval", r.icdev, uintptr(addr), uintptr(unsafe.Pointer(&value)))
	if err != nil {
		return 0, fmt.Errorf("dc_readval failed: %w", err)
	}

	if ret != 0 {
		return 0, fmt.Errorf("dc_readval returned %d", ret)
	}

	return value, nil
}

// KeyFromHex converts a hex string to a [6]byte key
func KeyFromHex(hexStr string) ([6]byte, error) {
	var key [6]byte
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return key, fmt.Errorf("invalid hex key: %w", err)
	}
	if len(bytes) != 6 {
		return key, fmt.Errorf("key must be exactly 6 bytes (got %d)", len(bytes))
	}
	copy(key[:], bytes)
	return key, nil
}

// DataToHex converts a [16]byte array to a hex string
func DataToHex(data [16]byte) string {
	return hex.EncodeToString(data[:])
}

// DataFromHex converts a hex string to a [16]byte array
func DataFromHex(hexStr string) ([16]byte, error) {
	var data [16]byte
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return data, fmt.Errorf("invalid hex data: %w", err)
	}
	if len(bytes) != 16 {
		return data, fmt.Errorf("data must be exactly 16 bytes (got %d)", len(bytes))
	}
	copy(data[:], bytes)
	return data, nil
}
