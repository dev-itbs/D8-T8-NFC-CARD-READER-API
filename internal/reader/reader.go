package reader

import (
	"encoding/hex"
	"fmt"
	"log"
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
	log.Printf("[READER] Initializing reader: dll=%s, port=%d, baud=%d", dllName, port, baud)
	loader, err := NewDLLLoader(dllName)
	if err != nil {
		log.Printf("[READER] ERROR: Failed to load DLL: %v", err)
		return nil, err
	}

	// Call dc_init(port, baud)
	log.Printf("[READER] Calling dc_init...")
	ret, err := loader.Call("dc_init", uintptr(port), uintptr(baud))
	if err != nil {
		log.Printf("[READER] ERROR: dc_init failed: %v", err)
		loader.Close()
		return nil, fmt.Errorf("dc_init failed: %w", err)
	}

	if ret <= 0 {
		log.Printf("[READER] ERROR: dc_init returned invalid value: %d", ret)
		loader.Close()
		return nil, fmt.Errorf("dc_init returned %d (expected > 0)", ret)
	}

	log.Printf("[READER] Initialized successfully with icdev=%d", ret)
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

// CardInfo holds the raw identification values returned during card detection.
type CardInfo struct {
	SNR  uint32 // 4-byte serial number from dc_anticoll
	ATQA uint16 // Answer To reQuest Type A from dc_request
	SAK  uint8  // Select AcKnowledge from dc_select (same value as ISO 14443-3 SAK)
}

// DetectCardInfo runs the full detect workflow and returns ATQA + SAK in addition to SNR.
func (r *Reader) DetectCardInfo(mode int) (CardInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var info CardInfo

	var tagType uint16
	ret, err := r.loader.Call("dc_request", r.icdev, uintptr(mode), uintptr(unsafe.Pointer(&tagType)))
	if err != nil {
		return info, fmt.Errorf("dc_request failed: %w", err)
	}
	if ret != 0 {
		return info, fmt.Errorf("dc_request returned %d (no card detected)", ret)
	}
	info.ATQA = tagType

	var snr uint32
	ret, err = r.loader.Call("dc_anticoll", r.icdev, uintptr(0), uintptr(unsafe.Pointer(&snr)))
	if err != nil {
		return info, fmt.Errorf("dc_anticoll failed: %w", err)
	}
	if ret != 0 {
		return info, fmt.Errorf("dc_anticoll returned %d", ret)
	}
	info.SNR = snr

	var size uint8
	ret, err = r.loader.Call("dc_select", r.icdev, uintptr(snr), uintptr(unsafe.Pointer(&size)))
	if err != nil {
		return info, fmt.Errorf("dc_select failed: %w", err)
	}
	if ret != 0 {
		return info, fmt.Errorf("dc_select returned %d", ret)
	}
	info.SAK = size

	return info, nil
}

// DetectCard detects a card using the proper workflow: request -> anticoll -> select
func (r *Reader) DetectCard(mode int) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Printf("[READER] DetectCard: Starting card detection workflow, mode=%d", mode)

	// Step 1: Request card
	var tagType uint16
	ret, err := r.loader.Call("dc_request", r.icdev, uintptr(mode), uintptr(unsafe.Pointer(&tagType)))
	if err != nil {
		log.Printf("[READER] dc_request ERROR: %v", err)
		return 0, fmt.Errorf("dc_request failed: %w", err)
	}
	if ret != 0 {
		log.Printf("[READER] dc_request failed: ret=%d (no card present or communication error)", ret)
		return 0, fmt.Errorf("dc_request returned %d (no card detected)", ret)
	}
	log.Printf("[READER] Card requested, tag type=%04X", tagType)

	// Step 2: Anti-collision
	var snr uint32
	ret, err = r.loader.Call("dc_anticoll", r.icdev, uintptr(0), uintptr(unsafe.Pointer(&snr)))
	if err != nil {
		log.Printf("[READER] dc_anticoll ERROR: %v", err)
		return 0, fmt.Errorf("dc_anticoll failed: %w", err)
	}
	if ret != 0 {
		log.Printf("[READER] dc_anticoll failed: ret=%d", ret)
		return 0, fmt.Errorf("dc_anticoll returned %d", ret)
	}
	log.Printf("[READER] Card anticoll successful, SNR=%08X", snr)

	// Step 3: Select card
	var size uint8
	ret, err = r.loader.Call("dc_select", r.icdev, uintptr(snr), uintptr(unsafe.Pointer(&size)))
	if err != nil {
		log.Printf("[READER] dc_select ERROR: %v", err)
		return 0, fmt.Errorf("dc_select failed: %w", err)
	}
	if ret != 0 {
		log.Printf("[READER] dc_select failed: ret=%d", ret)
		return 0, fmt.Errorf("dc_select returned %d", ret)
	}
	log.Printf("[READER] Card selected successfully, size=%d", size)

	return snr, nil
}

// LoadKey loads an authentication key into the reader's RAM
// mode: 0-2 for KEY A, 4-6 for KEY B
func (r *Reader) LoadKey(mode, sector int, key [6]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	keyHex := fmt.Sprintf("%02X%02X%02X%02X%02X%02X", key[0], key[1], key[2], key[3], key[4], key[5])
	log.Printf("[READER] LoadKey: mode=%d, sector=%d, key=%s", mode, sector, keyHex)

	ret, err := r.loader.Call("dc_load_key", r.icdev, uintptr(mode), uintptr(sector), uintptr(unsafe.Pointer(&key[0])))
	if err != nil {
		log.Printf("[READER] LoadKey ERROR: %v", err)
		return fmt.Errorf("dc_load_key failed: %w", err)
	}

	if ret != 0 {
		log.Printf("[READER] LoadKey failed: ret=%d (key may be incorrect)", ret)
		return fmt.Errorf("dc_load_key returned %d", ret)
	}

	log.Printf("[READER] LoadKey successful")
	return nil
}

// Authenticate authenticates a sector with a pre-loaded key
func (r *Reader) Authenticate(mode, sector int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Printf("[READER] Authenticate: mode=%d, sector=%d", mode, sector)
	ret, err := r.loader.Call("dc_authentication", r.icdev, uintptr(mode), uintptr(sector))
	if err != nil {
		log.Printf("[READER] Authenticate ERROR: %v", err)
		return fmt.Errorf("dc_authentication failed: %w", err)
	}

	if ret != 0 {
		log.Printf("[READER] Authenticate failed: ret=%d (check key is correct for this card, default is FFFFFFFFFFFF)", ret)
		return fmt.Errorf("dc_authentication returned %d (wrong key or invalid sector)", ret)
	}

	log.Printf("[READER] Authenticate successful")
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
// addr: Block address (0-63). Calculate from: block_addr = sector * 4 + block_within_sector
func (r *Reader) ReadBlock(addr int) ([16]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Printf("[READER] ReadBlock: addr=%d (sector=%d, block_in_sector=%d)", addr, addr/4, addr%4)
	var data [16]byte
	ret, err := r.loader.Call("dc_read", r.icdev, uintptr(addr), uintptr(unsafe.Pointer(&data[0])))
	if err != nil {
		log.Printf("[READER] ReadBlock ERROR: %v", err)
		return data, fmt.Errorf("dc_read failed: %w", err)
	}

	if ret != 0 {
		log.Printf("[READER] ReadBlock failed: ret=%d (block protected or authentication failed)", ret)
		return data, fmt.Errorf("dc_read returned %d (ensure authentication succeeded)", ret)
	}

	log.Printf("[READER] ReadBlock successful: %s", DataToHex(data))
	return data, nil
}

// ChangeBlock3 updates the sector trailer (block 3) using dc_changeb3.
// This is the recommended DLL function for updating sector keys and access bits.
// Requires prior authentication with Authenticate() for the sector.
//
//   - secNr:    sector number (1-15)
//   - keyA:     new 6-byte KEY A to write
//   - b0..b3:   trailer bytes 6-9 (3 access-condition bytes + user/GPB byte)
//   - bk:       supplementary byte — pass 0 (unused on most firmware versions)
//   - keyB:     new 6-byte KEY B to write
func (r *Reader) ChangeBlock3(secNr int, keyA [6]byte, b0, b1, b2, b3, bk byte, keyB [6]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Printf("[READER] ChangeBlock3: sector=%d, keyA=%X, access=[%02X %02X %02X %02X], bk=%02X, keyB=%X",
		secNr, keyA, b0, b1, b2, b3, bk, keyB)

	ret, err := r.loader.Call("dc_changeb3", r.icdev, uintptr(secNr),
		uintptr(unsafe.Pointer(&keyA[0])),
		uintptr(b0), uintptr(b1), uintptr(b2), uintptr(b3), uintptr(bk),
		uintptr(unsafe.Pointer(&keyB[0])))
	if err != nil {
		log.Printf("[READER] ChangeBlock3 ERROR: %v", err)
		return fmt.Errorf("dc_changeb3 failed: %w", err)
	}
	if ret != 0 {
		log.Printf("[READER] ChangeBlock3 failed: ret=%d (check authentication and access bits)", ret)
		return fmt.Errorf("dc_changeb3 returned %d", ret)
	}

	log.Printf("[READER] ChangeBlock3 successful")
	return nil
}

// WriteBlock writes 16 bytes to a block
// addr: Block address (0-63). Calculate from: block_addr = sector * 4 + block_within_sector
func (r *Reader) WriteBlock(addr int, data [16]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Printf("[READER] WriteBlock: addr=%d (sector=%d, block_in_sector=%d), data=%s",
		addr, addr/4, addr%4, DataToHex(data))

	ret, err := r.loader.Call("dc_write", r.icdev, uintptr(addr), uintptr(unsafe.Pointer(&data[0])))
	if err != nil {
		log.Printf("[READER] WriteBlock ERROR: %v", err)
		return fmt.Errorf("dc_write failed: %w", err)
	}

	if ret != 0 {
		log.Printf("[READER] WriteBlock failed: ret=%d (block read-only, protected, or auth failed)", ret)
		return fmt.Errorf("dc_write returned %d (block may be read-only or unauth)", ret)
	}

	log.Printf("[READER] WriteBlock successful")
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
