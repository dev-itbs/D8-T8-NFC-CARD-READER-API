# Getting Started with D8/T8 Card Reader API

Welcome! This is a complete Go REST API application for reading and writing contactless smart cards using the D8/T8 card reader device.

## What's Included

✅ **Complete Go Application** (Go 1.22 LTS)  
✅ **REST API** with 14 endpoints for card and device operations  
✅ **Production-Ready Code** with best practices  
✅ **Comprehensive Documentation** (README, API docs, installation guide)  
✅ **Postman Collection** for easy testing  
✅ **No CGO Required** - pure Go using Windows syscalls  
✅ **Thread-Safe** - mutex-protected device access  

## 5-Minute Quick Start

### Prerequisites
- Windows OS
- Go 1.22+ ([Download](https://go.dev/dl/))
- D8/T8 device connected via USB
- `dc_sdk.dll` from the device SDK

### Steps

**1. Navigate to project:**
```bash
cd D:\PROJECTS\LTO\READER-API
```

**2. Place the DLL:**
Copy `dc_sdk.dll` to this directory or set environment variable:
```bash
set DLL_NAME=path\to\dc_sdk.dll
```

**3. Build:**
```bash
go build -o server.exe ./cmd/server
```

**4. Run:**
```bash
.\server.exe
```

**5. Test:**
```bash
curl http://localhost:8080/health
```

You should get:
```json
{
  "status": "ok",
  "name": "D8/T8 Card Reader API",
  "version": "1.0.0"
}
```

Done! 🎉

## Project Structure

```
D:\PROJECTS\LTO\READER-API\
├── cmd/server/main.go              ← Server entry point
├── internal/
│   ├── reader/                      ← DLL wrapper & reader service
│   │   ├── dll.go                   ← Windows DLL loading
│   │   └── reader.go                ← Reader API methods
│   ├── api/                         ← REST API
│   │   ├── router.go                ← Chi router setup
│   │   └── handlers/                ← HTTP handlers
│   │       ├── health.go
│   │       ├── card.go              ← Read/write/detect
│   │       └── device.go            ← Device operations
│   └── models/                      ← Request/response types
├── config/config.go                 ← Configuration management
├── postman/                         ← Postman collection
├── README.md                        ← Full API documentation
├── INSTALLATION.md                  ← Detailed setup guide
└── GETTING_STARTED.md              ← This file
```

## Key Features

### Card Operations
- **Detect** - Find cards and get their serial numbers
- **Read** - Read 16-byte blocks with authentication
- **Write** - Write 16-byte blocks with authentication
- **Halt** - Stop card communication

### Device Operations
- **Version** - Get firmware version
- **Beep** - Control the buzzer
- **Reset** - RF reset
- **EEPROM** - Read/write device memory
- **Value Blocks** - Initialize, increment, decrement counters

## API Examples

### Detect a Card
```bash
curl -X POST http://localhost:8080/api/v1/card/detect \
  -H "Content-Type: application/json" \
  -d '{"mode": 0}'
```

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

### Read a Block
```bash
curl -X POST http://localhost:8080/api/v1/card/read \
  -H "Content-Type: application/json" \
  -d '{
    "mode": 0,
    "sector": 0,
    "block": 1,
    "key_mode": 0,
    "key": "FFFFFFFFFFFF"
  }'
```

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

### Write a Block
```bash
curl -X POST http://localhost:8080/api/v1/card/write \
  -H "Content-Type: application/json" \
  -d '{
    "mode": 0,
    "sector": 0,
    "block": 1,
    "key_mode": 0,
    "key": "FFFFFFFFFFFF",
    "data": "48656C6C6F576F726C6421000000000"
  }'
```

## Using Postman

Import the included Postman collection for easy API testing:

1. Open Postman
2. Click **Import** → **Upload Files**
3. Select: `postman\D8T8-CardReader.postman_collection.json`
4. All 14 endpoints are pre-configured
5. Set `base_url` variable to `http://localhost:8080`

## Configuration

Control behavior via environment variables (all optional):

| Variable | Default | Purpose |
|----------|---------|---------|
| `READER_PORT` | `100` | Port: 100=USB, 0-3=COM1-COM4 |
| `READER_BAUD` | `115200` | Baud rate (9600-115200) |
| `SERVER_ADDR` | `:8080` | HTTP server address |
| `DLL_NAME` | `dc_sdk.dll` | DLL file name/path |
| `GRACEFUL_WAIT` | `5` | Shutdown grace period (seconds) |

Example:
```bash
set READER_PORT=100
set READER_BAUD=115200
set SERVER_ADDR=:8080
.\server.exe
```

## Default Card Keys

Most Mifare cards come with these default keys:

```
KEY A: FFFFFFFFFFFF
KEY B: FFFFFFFFFFFF
```

Use these values in the `"key"` field of API requests.

## Common Card Types Supported

✅ Mifare Classic 1K/4K  
✅ Mifare Ultralight  
✅ Mifare DESFire  
✅ ISO14443 Type A/B  
✅ ISO15693  
✅ Many others (see reference manual)

## Best Practices

1. **Always halt cards** after operations (`POST /api/v1/card/halt`)
2. **Use IDLE mode** (mode=0) for single card operations
3. **Handle errors** - implement retry logic with backoff
4. **Validate input** - especially hex strings and addresses
5. **Test with Postman** before integrating into production code
6. **Run as Administrator** if you encounter permission issues

## Troubleshooting

### DLL Load Error
- Place `dc_sdk.dll` in the same directory as `server.exe`
- Or set: `set DLL_NAME=C:\full\path\to\dc_sdk.dll`

### Device Not Detected
- Check USB connection
- Verify device in Device Manager
- Try: `set READER_PORT=100 && .\server.exe`

### Card Not Detected
- Ensure card is compatible (Mifare, ISO14443, etc.)
- Check card proximity to reader
- Try different modes (0=IDLE, 1=ALL)

### Port Already in Use
```bash
set SERVER_ADDR=:8081
.\server.exe
```

## What's Next?

1. **Read the full docs:** [README.md](README.md)
2. **Detailed setup:** [INSTALLATION.md](INSTALLATION.md)
3. **API reference:** [README.md → API Endpoints](README.md#api-endpoints)
4. **D8/T8 manual:** `D:\PROJECTS\LTO\CORE-PLATFORM\_assets\docs\Card Reader - D8&T8 reference manual.md`

## Technology Stack

- **Language:** Go 1.22 (LTS)
- **HTTP Router:** Chi v5
- **Device Interface:** Windows syscall.LoadDLL (no CGO)
- **Protocol:** RESTful JSON API
- **Concurrency:** Mutex-protected serialization

## File Sizes

- Binary (`server.exe`): ~7-8 MB
- DLL (`dc_sdk.dll`): ~500 KB (varies)
- Total deployment: ~8.5 MB

## License & Attribution

This project is proprietary to Dev ITBS.

Built on the D8/T8 SDK reference manual.

## Support

- **API Documentation:** `README.md`
- **Installation Guide:** `INSTALLATION.md`
- **Reference Manual:** `D:\PROJECTS\LTO\CORE-PLATFORM\_assets\docs\Card Reader - D8&T8 reference manual.md`

---

**Ready to go?** Run `.\server.exe` and start making requests! 🚀
