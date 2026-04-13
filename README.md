# D8/T8 Card Reader API

A Go REST API for reading and writing contactless smart cards using the D8/T8 card reader device. The API wraps the DC SDK DLL to provide HTTP endpoints for card operations.

## Features

- **Card Detection**: Detect cards and retrieve their serial numbers
- **Card Read/Write**: Read and write 16-byte blocks from/to cards with authentication
- **Device Control**: Beep, reset, and manage device settings
- **EEPROM Management**: Read/write device EEPROM
- **Value Blocks**: Initialize, increment, decrement, and read value blocks
- **RESTful API**: Clean JSON-based REST endpoints
- **Cross-platform**: No CGO required (uses syscall.LoadDLL on Windows)

## Requirements

- Go 1.22 LTS or later
- Windows OS (device only works on Windows)
- `dc_sdk.dll` (included with the D8/T8 SDK)

## Installation & Setup

### 1. Clone and Build

```bash
cd D:\PROJECTS\LTO\READER-API
go mod download
go build ./cmd/server
```

### 2. Prepare DLL

Place the `dc_sdk.dll` file (from the D8/T8 SDK) in the same directory as the compiled binary or set the `DLL_NAME` environment variable.

### 3. Run the Server

```bash
.\server.exe
```

Or with custom configuration:

```bash
set READER_PORT=100
set READER_BAUD=115200
set SERVER_ADDR=:8080
set DLL_NAME=dc_sdk.dll
set GRACEFUL_WAIT=5
.\server.exe
```

## Configuration

Set via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `READER_PORT` | `100` | Port: 0-3 for COM1-COM4, 100 for USB |
| `READER_BAUD` | `115200` | Baud rate: 9600-115200 |
| `SERVER_ADDR` | `:8080` | HTTP server bind address |
| `DLL_NAME` | `dc_sdk.dll` | Path to the DC SDK DLL |
| `GRACEFUL_WAIT` | `5` | Graceful shutdown timeout (seconds) |
| `BRANCA_SALT` | `""` | Salt used for SHA256-based Branca key derivation (`-sha` endpoints) |

## API Endpoints

### Health Check

**GET** `/health`

Health status of the API.

**Response:**
```json
{
  "status": "ok",
  "name": "D8/T8 Card Reader API",
  "version": "1.0.0"
}
```

---

### Card Operations

#### Detect Card

**POST** `/api/v1/card/detect`

Detect a card and return its serial number.

**Request:**
```json
{
  "mode": 0
}
```

**Parameters:**
- `mode`: 0 = IDLE mode, 1 = ALL mode

**Response:**
```json
{
  "success": true,
  "data": {
    "snr_hex": "0x1A2B3C4D",
    "snr_decimal": 439410765
  }
}
```

---

#### Read Card Block

**POST** `/api/v1/card/read`

Read 16 bytes from a card block after authentication.

**Request:**
```json
{
  "mode": 0,
  "sector": 1,
  "block": 4,
  "key_mode": 0,
  "key": "FFFFFFFFFFFF",
  "use_pass": false
}
```

**Parameters:**
- `mode`: 0 = IDLE, 1 = ALL
- `sector`: Sector number (0-15)
- `block`: Block address (0-63)
- `key_mode`: 0-2 for KEY A, 4-6 for KEY B
- `key`: Hex string for 6-byte key (e.g., "FFFFFFFFFFFF")
- `use_pass`: If true, uses `dc_authentication_pass` (no separate load_key)

**Response:**
```json
{
  "success": true,
  "data": {
    "data": "000102030405060708090A0B0C0D0E0F",
    "data_bytes": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15]
  }
}
```

---

#### Write Card Block

**POST** `/api/v1/card/write`

Write 16 bytes to a card block after authentication.

**Request:**
```json
{
  "mode": 0,
  "sector": 1,
  "block": 4,
  "key_mode": 0,
  "key": "FFFFFFFFFFFF",
  "data": "000102030405060708090A0B0C0D0E0F",
  "use_pass": false
}
```

**Parameters:**
- `mode`, `sector`, `block`, `key_mode`, `key`, `use_pass`: See Read endpoint
- `data`: Hex string for 16-byte data (32 characters)

**Response:**
```json
{
  "success": true,
  "data": {}
}
```

---

#### Read & Decode — MD5 (Branca)

**POST** `/api/v1/card/read-decoded-md5`

Reads all card blocks, extracts the NDEF Branca token, and decodes it in one request.  
Key derivation: `hex(MD5(snr_decimal))`

**Request:**
```json
{
  "mode": 0,
  "key_mode": 0,
  "key": "D3F7D3F7D3F7",
  "use_pass": false
}
```

**Response:**
```json
{
  "success": true,
  "message": "Card read and decoded successfully",
  "data": { "<decoded JSON payload>" }
}
```

---

#### Encode & Write — MD5 (Branca)

**POST** `/api/v1/card/write-encoded-md5`

Detects the card, derives a Branca key from the SNR (`hex(MD5(snr_decimal))`), encodes the JSON as a Branca token, and writes it to the card as an NDEF text record.

**Request:**
```json
{
  "mode": 0,
  "key_mode": 0,
  "key": "D3F7D3F7D3F7",
  "use_pass": true,
  "data": {
    "date_issued": "2026-02-23",
    "owner_name": "Maria Beatriz Alexa Miranda",
    "plate_number": "PEQ705",
    "engine_number": "4M41UCAU848",
    "chassis_number": "MMKYDJTFVFSR74",
    "vin": "MMBJNKB40AD012345",
    "file_number": "1901-000987654320",
    "vehicle_details": {
      "category": "Pickup Truck",
      "body_type": "Pickup Truck - 4 Door",
      "gross_weight": 2370,
      "net_weight": 1900,
      "max_power_kw": null,
      "series": "012345"
    },
    "specifications": {
      "color": "Red",
      "make_brand": "Mitsubishi",
      "year_model": 2010,
      "fuel_type": "Diesel",
      "classification": "Private",
      "vehicle_type": "LCV",
      "year_rebuilt": null,
      "piston_displacement_cc": 2396,
      "passenger_capacity": 5
    },
    "encumbrance": {
      "encumbered_to": "BDO Unibank"
    }
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "Card encoded and written successfully. 44 blocks written.",
  "data": {
    "snr_hex": "0xA8920A2F",
    "snr_decimal": 2828143151,
    "branca_token": "<encoded token>",
    "bytes_written": 704,
    "blocks_written": 44
  }
}
```

**Notes:**
- Key per-card: `hex(MD5(string(snr_decimal)))`
- Max payload: ~704 bytes on a Mifare Classic 1K card
- Verify with `read-decoded-md5` after writing

---

#### Decode Branca Token — MD5

**POST** `/api/v1/card/decode-md5`

Offline decode a Branca token without touching the card.  
Key derivation: `hex(MD5(snr_decimal))`

**Request:**
```json
{
  "branca_token": "<token string>",
  "snr_decimal": 2828143151
}
```

**Response:**
```json
{
  "success": true,
  "message": "Branca token decoded successfully",
  "data": {
    "snr_decimal": 2828143151,
    "branca_token": "<token string>",
    "payload": { "<decoded JSON>" }
  }
}
```

---

#### Read & Decode — SHA256+Salt (Branca)

**POST** `/api/v1/card/read-decoded-sha`

Same as `read-decoded-md5` but uses a stronger key derived with SHA256 and the server-side salt.  
Key derivation: `SHA256(snr_decimal + BRANCA_SALT)` → 32 raw bytes

**Request:**
```json
{
  "mode": 0,
  "key_mode": 0,
  "key": "D3F7D3F7D3F7",
  "use_pass": false
}
```

**Response:** Same shape as `read-decoded-md5`.

---

#### Encode & Write — SHA256+Salt (Branca)

**POST** `/api/v1/card/write-encoded-sha`

Same as `write-encoded-md5` but uses SHA256+salt key derivation. Requires `BRANCA_SALT` to be set in the environment — cards written with this endpoint can only be decoded by a server with the same salt.

**Request:** Same shape as `write-encoded-md5`.

**Response:** Same shape as `write-encoded-md5`.

**Notes:**
- Key per-card: `SHA256(string(snr_decimal) + BRANCA_SALT)`
- The salt never leaves the server; cards are unreadable without it
- Verify with `read-decoded-sha` after writing

---

#### Decode Branca Token — SHA256+Salt

**POST** `/api/v1/card/decode-sha`

Offline decode a Branca token encoded with the SHA256+salt key.  
Key derivation: `SHA256(snr_decimal + BRANCA_SALT)`

**Request:**
```json
{
  "branca_token": "<token string>",
  "snr_decimal": 2828143151
}
```

**Response:** Same shape as `decode-md5`.

---

#### Halt Card

**POST** `/api/v1/card/halt`

Halt the card. Must be called after each card operation.

**Response:**
```json
{
  "success": true,
  "data": {}
}
```

---

### Device Operations

#### Get Firmware Version

**GET** `/api/v1/device/version`

Get the device firmware version.

**Response:**
```json
{
  "success": true,
  "data": {
    "version": "2.5.3"
  }
}
```

---

#### Beep

**POST** `/api/v1/device/beep`

Trigger the device buzzer.

**Request:**
```json
{
  "ms": 100
}
```

**Parameters:**
- `ms`: Duration in milliseconds

**Response:**
```json
{
  "success": true,
  "data": {}
}
```

---

#### RF Reset

**POST** `/api/v1/device/reset`

Perform an RF reset on the device.

**Request:**
```json
{
  "ms": 2
}
```

**Response:**
```json
{
  "success": true,
  "data": {}
}
```

---

#### Read EEPROM

**POST** `/api/v1/device/eeprom/read`

Read data from device EEPROM.

**Request:**
```json
{
  "offset": 0,
  "length": 64
}
```

**Parameters:**
- `offset`: Start address (0-383)
- `length`: Bytes to read (1-384)

**Response:**
```json
{
  "success": true,
  "data": {
    "data": "48656C6C6F...",
    "bytes": [72, 101, 108, 108, 111, ...]
  }
}
```

---

#### Write EEPROM

**POST** `/api/v1/device/eeprom/write`

Write data to device EEPROM.

**Request:**
```json
{
  "offset": 0,
  "data": "48656C6C6F"
}
```

**Parameters:**
- `offset`: Start address (0-383)
- `data`: Hex string of data to write

**Response:**
```json
{
  "success": true,
  "data": {}
}
```

---

### Value Block Operations

#### Initialize Value Block

**POST** `/api/v1/device/value/init`

Initialize a value block.

**Request:**
```json
{
  "block": 10,
  "value": 1000
}
```

---

#### Increment Value Block

**POST** `/api/v1/device/value/increment`

Increment a value block.

**Request:**
```json
{
  "block": 10,
  "value": 100
}
```

---

#### Decrement Value Block

**POST** `/api/v1/device/value/decrement`

Decrement a value block.

**Request:**
```json
{
  "block": 10,
  "value": 50
}
```

---

#### Read Value Block

**POST** `/api/v1/device/value/read`

Read a value from a value block.

**Request:**
```json
{
  "block": 10
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "value": 1050
  }
}
```

---

## Error Responses

All errors return a JSON response with `success: false`:

```json
{
  "success": false,
  "message": "Failed to detect card: dc_card returned 1 (no card or error)",
  "code": 500
}
```

**HTTP Status Codes:**
- `200`: Success
- `400`: Bad request (invalid input)
- `500`: Server error (device operation failed)
- `503`: Service unavailable (device not initialized)

---

## Default Key Values

Most Mifare cards ship with default keys:

**KEY A (default):** `FFFFFFFFFFFF`  
**KEY B (default):** `FFFFFFFFFFFF`

---

## Example Usage

### Using cURL

```bash
# Health check
curl http://localhost:8080/health

# Detect a card
curl -X POST http://localhost:8080/api/v1/card/detect \
  -H "Content-Type: application/json" \
  -d '{"mode": 0}'

# Read a block
curl -X POST http://localhost:8080/api/v1/card/read \
  -H "Content-Type: application/json" \
  -d '{
    "mode": 0,
    "sector": 1,
    "block": 4,
    "key_mode": 0,
    "key": "FFFFFFFFFFFF",
    "use_pass": false
  }'

# Write a block
curl -X POST http://localhost:8080/api/v1/card/write \
  -H "Content-Type: application/json" \
  -d '{
    "mode": 0,
    "sector": 1,
    "block": 4,
    "key_mode": 0,
    "key": "FFFFFFFFFFFF",
    "data": "000102030405060708090A0B0C0D0E0F"
  }'
```

### Using Postman

Import the `postman/D8T8-CardReader.postman_collection.json` file into Postman for a pre-configured collection with all endpoints.

---

## Project Structure

```
D:\PROJECTS\LTO\READER-API\
├── cmd/
│   └── server/
│       └── main.go              # Entry point, server initialization
├── config/
│   └── config.go                # Configuration management
├── internal/
│   ├── reader/
│   │   ├── dll.go               # DLL loading and function pointers
│   │   └── reader.go            # Reader service wrapper
│   ├── api/
│   │   ├── router.go            # Chi router configuration
│   │   └── handlers/
│   │       ├── health.go        # Health check handler
│   │       ├── card.go          # Card operation handlers
│   │       └── device.go        # Device operation handlers
│   └── models/
│       ├── request.go           # Request structures
│       └── response.go          # Response structures
├── postman/
│   └── D8T8-CardReader.postman_collection.json  # Postman collection
├── go.mod
├── go.sum
└── README.md
```

---

## Best Practices

1. **Always call `/card/halt` after card operations** to prevent the reader from getting stuck
2. **Use IDLE mode (0) for single card operations** to avoid detecting multiple cards
3. **Test device connectivity** with `/health` before starting card operations
4. **Validate input parameters** (sector, block, key format) before making requests
5. **Handle errors gracefully** and retry with appropriate timeouts
6. **Use the Postman collection** for testing before integrating into your application

---

## Troubleshooting

### DLL Not Found
- Ensure `dc_sdk.dll` is in the same directory as the binary or set `DLL_NAME` env var with full path
- Verify the DLL is compatible with your Windows architecture (32-bit/64-bit)

### Device Not Detected
- Check USB connection
- Verify `READER_PORT=100` for USB connection (0-3 for COM ports)
- Try different baud rates if communication issues occur

### Card Detection Fails
- Ensure the card is compatible (Mifare, ISO14443, etc.)
- Check card proximity to the reader
- Verify key mode matches the card's key configuration

### Port Already in Use
- Change `SERVER_ADDR` to a different port (e.g., `:8081`)
- Kill any existing server process

---

## License

Proprietary - Dev ITBS

## Support

For issues related to the D8/T8 device, consult the device reference manual at:
`D:\PROJECTS\LTO\CORE-PLATFORM\_assets\docs\Card Reader - D8&T8 reference manual.md`
