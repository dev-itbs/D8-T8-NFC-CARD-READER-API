# Installation & Quick Start Guide

## Prerequisites

- **Windows OS** (required for D8/T8 hardware)
- **Go 1.22 LTS** or later ([Download](https://go.dev/dl/))
- **D8/T8 Card Reader** device
- **dc_sdk.dll** (included in D8/T8 SDK package)

## Step-by-Step Installation

### 1. Verify Go Installation

```bash
go version
```

Expected output: `go version go1.22.x windows/amd64` (or `windows/386`)

### 2. Navigate to Project Directory

```bash
cd D:\PROJECTS\LTO\READER-API
```

### 3. Download Dependencies

```bash
go mod download
```

### 4. Obtain the DLL

From the D8/T8 SDK, locate `dc_sdk.dll`:
- Copy it to the project root directory, or
- Copy it to the same directory as the compiled binary, or
- Set the environment variable:
  ```bash
  set DLL_NAME=C:\path\to\dc_sdk.dll
  ```

### 5. Build the Application

```bash
go build -o server.exe ./cmd/server
```

Or for 32-bit systems:
```bash
set GOARCH=386
go build -o server.exe ./cmd/server
```

A file named `server.exe` will be created in the current directory.

### 6. Configure the Environment (Optional)

Default configuration works for USB connection on port 8080:

```bash
# Optional: Custom configuration
set READER_PORT=100            # 100=USB, 0-3=COM1-COM4
set READER_BAUD=115200         # Baud rate
set SERVER_ADDR=:8080          # HTTP server address
set DLL_NAME=dc_sdk.dll        # DLL filename or path
set GRACEFUL_WAIT=5            # Shutdown grace period (seconds)
```

### 7. Connect the D8/T8 Device

- Plug the USB cable into your Windows machine
- If it's the first time, the OS may install drivers automatically
- Wait for the device to be recognized

### 8. Run the Server

```bash
.\server.exe
```

You should see:
```
2026/04/08 15:30:45 Starting D8/T8 Card Reader API
2026/04/08 15:30:45 Reader Port: 100, Baud: 115200
2026/04/08 15:30:45 Server Address: :8080
2026/04/08 15:30:45 DLL Name: dc_sdk.dll
2026/04/08 15:30:45 Card reader initialized successfully
2026/04/08 15:30:45 Server starting on :8080
```

### 9. Test the API

In another terminal window:

```bash
# Test health check
curl http://localhost:8080/health

# Expected response:
# {"status":"ok","name":"D8/T8 Card Reader API","version":"1.0.0"}
```

## Using Postman

1. Open **Postman** (or download from [postman.com](https://www.postman.com/))
2. Click **Import** → **Upload Files**
3. Select: `D:\PROJECTS\LTO\READER-API\postman\D8T8-CardReader.postman_collection.json`
4. The collection is imported with all endpoints
5. Set environment variable in Postman: `base_url` = `http://localhost:8080`
6. Start making requests!

## Common Issues & Solutions

### Issue: DLL Not Found
```
Error: failed to load dc_sdk.dll
```

**Solution:**
- Verify the DLL file exists and is in the correct location
- Use absolute path: `set DLL_NAME=C:\path\to\dc_sdk.dll`
- Check that the DLL architecture matches your Go build (32-bit vs 64-bit)

### Issue: Device Not Found
```
Error: Failed to initialize reader
```

**Solution:**
- Check USB connection
- Verify device shows up in Device Manager
- Try different baud rates: `set READER_BAUD=9600` or `115200`
- For COM port: `set READER_PORT=0` (COM1), `1` (COM2), etc.

### Issue: Port Already in Use
```
listen tcp :8080: bind: An attempt was made to reuse a socket in a state...
```

**Solution:**
- Use a different port: `set SERVER_ADDR=:8081`
- Kill the existing process:
  ```bash
  taskkill /IM server.exe /F
  ```

### Issue: Permission Denied (USB Device)
**Solution:**
- Run the command prompt as Administrator
- Try different USB port on your computer

## Verifying the Installation

Create a test script `test.bat`:

```batch
@echo off
REM Test the API health
curl -s http://localhost:8080/health | findstr /C:"ok" >nul
if %errorlevel% equ 0 (
    echo ✓ Server is running and healthy
    exit /b 0
) else (
    echo ✗ Server is not responding
    exit /b 1
)
```

Run it:
```bash
test.bat
```

## Next Steps

1. **Read the full API documentation:** [README.md](README.md)
2. **Test card detection:** Place a card on the reader and call `/api/v1/card/detect`
3. **Read/write data:** Use `/api/v1/card/read` and `/api/v1/card/write` endpoints
4. **Integrate into your application:** Use the HTTP endpoints in your code

## Support & Documentation

- **D8/T8 Reference Manual:** `D:\PROJECTS\LTO\CORE-PLATFORM\_assets\docs\Card Reader - D8&T8 reference manual.md`
- **API Documentation:** [README.md](README.md)
- **Postman Collection:** `postman/D8T8-CardReader.postman_collection.json`

## Building for Distribution

To create a standalone executable with the DLL included:

```bash
REM Build the binary
go build -o reader-api.exe ./cmd/server

REM Copy DLL to the same directory
copy C:\path\to\dc_sdk.dll .

REM Now you can distribute reader-api.exe and dc_sdk.dll together
```

Users just need to run: `.\reader-api.exe`
