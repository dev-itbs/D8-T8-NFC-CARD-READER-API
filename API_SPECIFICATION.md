# D8/T8 Card Reader API - Complete Specification

Based on D8/T8 Reference Manual & dcrf32.dll implementation

## Overview

All endpoints follow the standard HTTP REST pattern with JSON request/response bodies.

---

## ⚠️ Important Notes

1. **Device Initialization**: The device is initialized automatically on server startup with `dc_init(port=100, baud=115200)`
2. **Card Halt**: Always call `/card/halt` after read/write operations
3. **IDLE vs ALL Mode**: 
   - Mode 0 (IDLE): For single card, requires card removal & re-insertion to detect again
   - Mode 1 (ALL): For multiple cards, card detected repeatedly
4. **Error Codes**: Non-zero return values indicate errors (see section below)

---

## Error Codes & Meanings

| Code | Name | Meaning |
|------|------|---------|
| 0 | SUCCESS | Operation successful |
| -1468203008 | NO_CARD | No card detected in reader |
| 1 | NO_CARD | No card found |
| Other | ERROR | Device or communication error |

**Note**: Specific error code mapping depends on dcrf32.dll implementation. Most non-zero values indicate failure.

---

## API Endpoints

### 1. Health Check
**GET** `/health`

Check API server status.

**Response** (200 OK):
```json
{
  "status": "ok",
  "name": "D8/T8 Card Reader API",
  "version": "1.0.0"
}
```

**Use Case**: Verify server is running before card operations.

---

### 2. Card Operations

#### 2.1 Detect Card
**POST** `/api/v1/card/detect`

Equivalent to: `dc_card(icdev, mode, &snr)`

**Reference**: D8/T8 Manual § dc_card (page 260)

**Request**:
```json
{
  "mode": 0
}
```

**Parameters**:
- `mode`: 
  - `0` = IDLE mode (single card, must remove & re-insert to detect again)
  - `1` = ALL mode (multiple cards, detects repeatedly)

**Response** (200 OK):
```json
{
  "success": true,
  "data": {
    "snr_hex": "0x1A2B3C4D",
    "snr_decimal": 439410765
  }
}
```

**Error Response** (500):
```json
{
  "success": false,
  "message": "Failed to detect card: dc_card returned -1468203008 (no card or error)",
  "code": 500
}
```

**Notes**:
- Returns card Serial Number (SNR)
- If no card: returns error code (typically -1468203008)
- Must call `/card/halt` after operation in IDLE mode

---

#### 2.2 Read Card Block
**POST** `/api/v1/card/read`

Equivalent to: `dc_load_key()` + `dc_authentication()` + `dc_read()`

**Reference**: D8/T8 Manual § dc_load_key, dc_authentication, dc_read (pages 340-393)

**Request**:
```json
{
  "mode": 0,
  "sector": 0,
  "block": 1,
  "key_mode": 0,
  "key": "FFFFFFFFFFFF"
}
```

**Parameters**:
- `mode`: 0=IDLE, 1=ALL
- `sector`: 0-15 (Mifare Classic has 16 sectors)
- `block`: 0-63 (block address within sector)
- `key_mode`: 
  - `0-2` = KEY A variants
  - `4-6` = KEY B variants
- `key`: 12-character hex string (6 bytes)
  - Default for most cards: `FFFFFFFFFFFF`

**Response** (200 OK):
```json
{
  "success": true,
  "data": {
    "data": "000102030405060708090A0B0C0D0E0F",
    "data_bytes": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15]
  }
}
```

**Error Response** (500):
```json
{
  "success": false,
  "message": "Authentication failed: dc_authentication returned 1",
  "code": 500
}
```

**Common Errors**:
- Wrong key → authentication fails
- Wrong sector/block → read fails
- Card not detected → no card in range
- Invalid key format → 400 Bad Request

**Notes**:
- Returns 16 bytes (32 hex characters)
- `data_bytes` array is for convenience
- Always call `/card/halt` after
- Mifare block 3 of each sector is the "trailer" (contains keys - read-only usually)

---

#### 2.3 Write Card Block
**POST** `/api/v1/card/write`

Equivalent to: `dc_load_key()` + `dc_authentication()` + `dc_write()`

**Reference**: D8/T8 Manual § dc_load_key, dc_authentication, dc_write (pages 340-397)

**Request**:
```json
{
  "mode": 0,
  "sector": 0,
  "block": 1,
  "key_mode": 0,
  "key": "FFFFFFFFFFFF",
  "data": "48656C6C6F576F726C6421000000000"
}
```

**Parameters**:
- Same as Read, plus:
- `data`: 32-character hex string (exactly 16 bytes)
  - Example: `"48656C6C6F576F726C6421000000000"` = "HelloWorld!..." in hex

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

**Error Response** (500):
```json
{
  "success": false,
  "message": "Write failed: dc_write returned 1",
  "code": 500
}
```

**Validation**:
- `data` must be exactly 32 hex characters (400 Bad Request if not)
- `block` must be 0-63 (400 Bad Request if not)
- Some blocks may be read-only (write fails silently)

**Notes**:
- Cannot write to block 3 of each sector (contains keys)
- Always call `/card/halt` after
- Data must be exactly 16 bytes

---

#### 2.4 Halt Card
**POST** `/api/v1/card/halt`

Equivalent to: `dc_halt(icdev)`

**Reference**: D8/T8 Manual § dc_halt (page 413-422)

**Request**:
```json
{}
```

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

**Important**:
- **MUST be called after every read/write operation**
- If using IDLE mode (mode=0), must call this or card won't be detected again
- If using ALL mode (mode=1), optional but recommended

---

### 3. Device Operations

#### 3.1 Get Firmware Version
**GET** `/api/v1/device/version`

Equivalent to: `dc_getver(icdev, buffer)`

**Reference**: D8/T8 Manual § dc_getver (page 738-749)

**Request**: None (GET request)

**Response** (200 OK):
```json
{
  "success": true,
  "data": {
    "version": "2.5.3"
  }
}
```

**Format**: Major.Minor.Patch (3-byte version)

**Use Case**: Verify device is connected and responding

---

#### 3.2 Device Beep
**POST** `/api/v1/device/beep`

Equivalent to: `dc_beep(icdev, ms)`

**Reference**: D8/T8 Manual § dc_beep (page 691-700)

**Request**:
```json
{
  "ms": 100
}
```

**Parameters**:
- `ms`: Duration in milliseconds (0-65535)

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

**Use Case**: Provide audio feedback on card detection/read/write

---

#### 3.3 RF Reset
**POST** `/api/v1/device/reset`

Equivalent to: `dc_reset(icdev, ms)`

**Reference**: D8/T8 Manual § dc_reset (page 848-860)

**Request**:
```json
{
  "ms": 2
}
```

**Parameters**:
- `ms`: Reset duration in milliseconds
  - `0` = Close radio frequency
  - `1-32767` = Reset duration

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

**Use Case**: Reset RF field if device becomes unresponsive

---

#### 3.4 Read EEPROM
**POST** `/api/v1/device/eeprom/read`

Equivalent to: `dc_srd_eeprom(icdev, offset, length, buffer)`

**Reference**: D8/T8 Manual § dc_srd_eeprom (page 786-804)

**Request**:
```json
{
  "offset": 0,
  "length": 64
}
```

**Parameters**:
- `offset`: Start address (0-383)
- `length`: Bytes to read (1-384)

**Response** (200 OK):
```json
{
  "success": true,
  "data": {
    "data": "48656C6C6F576F726C64...",
    "bytes": [72, 101, 108, 108, 111, 87, 111, 114, 108, 100]
  }
}
```

**Validation**:
- `offset` + `length` must not exceed 384 bytes

---

#### 3.5 Write EEPROM
**POST** `/api/v1/device/eeprom/write`

Equivalent to: `dc_swr_eeprom(icdev, offset, length, buffer)`

**Reference**: D8/T8 Manual § dc_swr_eeprom (page 806-824)

**Request**:
```json
{
  "offset": 0,
  "data": "48656C6C6F"
}
```

**Parameters**:
- `offset`: Start address (0-383)
- `data`: Hex string (variable length, max 384 bytes total)

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

---

### 4. Value Block Operations

Value blocks are special Mifare blocks used for counters/balance. Must be initialized first.

#### 4.1 Initialize Value Block
**POST** `/api/v1/device/value/init`

Equivalent to: `dc_initval(icdev, addr, value)`

**Reference**: D8/T8 Manual § dc_initval (page 473-491)

**Request**:
```json
{
  "block": 10,
  "value": 1000
}
```

**Parameters**:
- `block`: Block address (1-63, not 0)
- `value`: Initial value (0-4294967295)

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

**Important**: Must be called before increment/decrement/read on this block

---

#### 4.2 Increment Value Block
**POST** `/api/v1/device/value/increment`

Equivalent to: `dc_increment(icdev, addr, value)`

**Reference**: D8/T8 Manual § dc_increment (page 495-513)

**Request**:
```json
{
  "block": 10,
  "value": 100
}
```

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

---

#### 4.3 Decrement Value Block
**POST** `/api/v1/device/value/decrement`

Equivalent to: `dc_decrement(icdev, addr, value)`

**Reference**: D8/T8 Manual § dc_decrement (page 528-539)

**Request**:
```json
{
  "block": 10,
  "value": 50
}
```

**Response** (200 OK):
```json
{
  "success": true,
  "data": {}
}
```

---

#### 4.4 Read Value Block
**POST** `/api/v1/device/value/read`

Equivalent to: `dc_readval(icdev, addr, &value)`

**Reference**: D8/T8 Manual § dc_readval (page 515-527)

**Request**:
```json
{
  "block": 10
}
```

**Response** (200 OK):
```json
{
  "success": true,
  "data": {
    "value": 1050
  }
}
```

---

## Request/Response Standards

### Success Response Format
```json
{
  "success": true,
  "data": { /* endpoint-specific data */ }
}
```

### Error Response Format
```json
{
  "success": false,
  "message": "Human-readable error message",
  "code": 500
}
```

### HTTP Status Codes

| Code | Meaning | When |
|------|---------|------|
| 200 | OK | Operation successful |
| 400 | Bad Request | Invalid input (bad hex, wrong length, out of range) |
| 500 | Internal Server Error | Device operation failed |
| 503 | Service Unavailable | Device not initialized |

---

## Typical Workflow

### Read a Card Block

1. **Detect Card**
   ```bash
   POST /api/v1/card/detect
   {"mode": 0}
   ```
   → Get SNR

2. **Read Block**
   ```bash
   POST /api/v1/card/read
   {
     "mode": 0,
     "sector": 0,
     "block": 1,
     "key_mode": 0,
     "key": "FFFFFFFFFFFF"
   }
   ```
   → Get 16 bytes of data

3. **Halt Card** (IMPORTANT!)
   ```bash
   POST /api/v1/card/halt
   {}
   ```

### Write a Card Block

1. **Detect Card**
   ```bash
   POST /api/v1/card/detect
   {"mode": 0}
   ```

2. **Write Block**
   ```bash
   POST /api/v1/card/write
   {
     "mode": 0,
     "sector": 0,
     "block": 1,
     "key_mode": 0,
     "key": "FFFFFFFFFFFF",
     "data": "48656C6C6F576F726C6421000000000"
   }
   ```

3. **Halt Card**
   ```bash
   POST /api/v1/card/halt
   {}
   ```

---

## Mifare Classic Card Structure

### Sectors & Blocks
- **16 sectors** (0-15)
- **4 blocks per sector** (0-3)
- **Block 3** = Sector trailer (keys + access bits - usually read-only)
- **Blocks 0-2** = Data blocks (readable/writable)

### Default Keys
```
KEY A: FFFFFFFFFFFF
KEY B: FFFFFFFFFFFF
```

### Block 0 (Manufacturer)
- Read-only
- Contains UID and manufacturer data

---

## Configuration

### Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `READER_PORT` | `100` | Port: 100=USB, 0-3=COM1-COM4 |
| `READER_BAUD` | `115200` | Baud rate |
| `SERVER_ADDR` | `:8080` | HTTP listen address |
| `DLL_NAME` | `dc_sdk.dll` | DLL file name (change to `dcrf32.dll` if needed) |
| `GRACEFUL_WAIT` | `5` | Graceful shutdown timeout |

---

## Troubleshooting

### "No card or error" (-1468203008)
- Place card on reader
- Check card compatibility (Mifare, ISO14443)
- Try ALL mode instead of IDLE: `"mode": 1`
- Check RF field (try `/device/reset`)

### "Authentication failed"
- Wrong key for the card
- Wrong key_mode (0-2 for KEY A, 4-6 for KEY B)
- Card uses non-default keys

### "Write failed"
- Block 3 is read-only (contains keys)
- Card is write-protected
- Wrong key for write permission

### Device not responding
- Check USB connection
- Try `/device/version` to verify connection
- Restart server

---

## Reference

- **D8/T8 Reference Manual**: `D:\PROJECTS\LTO\CORE-PLATFORM\_assets\docs\Card Reader - D8&T8 reference manual.md`
- **DLL**: dcrf32.dll (from D8/T8 SDK)
- **API**: REST HTTP with JSON

---

*Last Updated: 2026-04-08*  
*Based on D8/T8 Reference Manual*  
*Compatible with dcrf32.dll*
