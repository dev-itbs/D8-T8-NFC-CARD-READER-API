# Troubleshooting Guide - D8/T8 Card Reader API

## Common Issues & Solutions

### Issue 1: "dc_card returned -1468203008 (no card or error)"

**Symptoms:**
```json
{
  "success": false,
  "message": "Failed to detect card: dc_card returned -1468203008 (no card or error)",
  "code": 500
}
```

**Causes & Solutions:**

#### ✅ Solution 1: No Card in Reader
- **Check**: Is there a card physically on the reader?
- **Fix**: Place a compatible card on the reader and try again
- **Compatible Cards**: Mifare Classic, Mifare Ultralight, ISO14443 Type A/B

#### ✅ Solution 2: Card Already in Halt Mode (IDLE mode issue)
- **Check**: Are you using mode=0 (IDLE)?
- **Symptom**: First detection works, second detection fails
- **Fix**: In IDLE mode, after calling `/card/halt`, you must **remove and re-insert the card**
- **Test**: Try with mode=1 (ALL mode) instead:
  ```json
  {"mode": 1}
  ```
  If this works, switch to ALL mode or remember to remove card between operations

#### ✅ Solution 3: RF Field Not Active
- **Fix**: Try resetting the RF field first:
  ```bash
  POST /api/v1/device/reset
  {"ms": 2}
  ```
- **Then**: Try detecting again

#### ✅ Solution 4: Device Not Properly Initialized
- **Check**: Device version to confirm connection
  ```bash
  GET /api/v1/device/version
  ```
- **Expected**: Shows version like "2.5.3"
- **If error**: Device may not be connected or DLL issue
  - Check USB connection
  - Check `DLL_NAME` environment variable is set to `dcrf32.dll`
  - Restart server

#### ✅ Solution 5: Wrong Port/Baud Configuration
- **Current**: Port 100 (USB), Baud 115200
- **Check**: Is device on USB or COM port?
  - **USB**: Keep READER_PORT=100 ✅
  - **COM1**: Set READER_PORT=0
  - **COM2**: Set READER_PORT=1
  - **COM3**: Set READER_PORT=2
  - **COM4**: Set READER_PORT=3
- **Try different baud rates**:
  ```bash
  set READER_BAUD=9600
  .\server.exe
  ```
  or
  ```bash
  set READER_BAUD=38400
  .\server.exe
  ```

#### ✅ Solution 6: Card Not Compatible
- **Check**: What type of card are you using?
- **Supported**: Mifare Classic 1K/4K, Mifare Ultralight, ISO14443 Type A/B, and others (see D8/T8 manual)
- **Not supported**: Some specialty cards, advanced encryption cards
- **Fix**: Try a different card (standard Mifare Classic is most reliable)

---

### Issue 2: "Authentication failed: dc_authentication returned 1"

**Symptoms:**
```json
{
  "success": false,
  "message": "Authentication failed: dc_authentication returned 1",
  "code": 500
}
```

**Causes & Solutions:**

#### ✅ Solution 1: Wrong Key
- **Check**: What key does your card use?
- **Default**: Most cards use `FFFFFFFFFFFF` for both KEY A and KEY B
- **Try**:
  ```json
  {
    "key": "FFFFFFFFFFFF",
    "key_mode": 0
  }
  ```
- **If still fails**: Card uses custom keys - you need the correct key

#### ✅ Solution 2: Wrong Key Mode
- **KEY A variants**: key_mode = 0, 1, or 2
- **KEY B variants**: key_mode = 4, 5, or 6
- **For standard cards**: Use key_mode = 0 (KEY A)
- **Try**: 
  ```json
  {"key_mode": 0}
  ```

#### ✅ Solution 3: Wrong Sector
- **Check**: What sector are you trying to access?
- **Mifare Classic**: Sectors 0-15
- **Block address** calculated from sector:
  - Sector 0, Block 0-3 = Card block 0-3
  - Sector 1, Block 0-3 = Card block 4-7
  - etc.
- **Example**: To read card block 5, use sector=1, block=1

#### ✅ Solution 4: Card Not Detected First
- **Check**: Did you call `/card/detect` first?
- **Order**:
  1. `/card/detect` ← detects card
  2. `/card/read` ← reads after detection
  3. `/card/halt` ← stops operation
- **If skipped**: Card not selected, authentication fails
- **Fix**: Always start with `/card/detect`


---

### Issue 3: "Write failed: dc_write returned 1"

**Symptoms:**
```json
{
  "success": false,
  "message": "Write failed: dc_write returned 1",
  "code": 500
}
```

**Causes & Solutions:**

#### ✅ Solution 1: Trying to Write Block 3 (Trailer)
- **Check**: Are you writing to block 3?
- **Problem**: Block 3 is the sector trailer (contains keys - usually read-only)
- **Fix**: Write to blocks 0, 1, or 2 instead:
  ```json
  {"block": 1}
  ```

#### ✅ Solution 2: Card Block is Write-Protected
- **Problem**: Card or block has write protection enabled
- **Fix**: Cannot bypass without the write key - try different block or card

#### ✅ Solution 3: Data Format Wrong
- **Check**: Is data exactly 32 hex characters (16 bytes)?
- **Wrong**: `"data": "48656C6C6F"` (too short)
- **Correct**: `"data": "48656C6C6F576F726C6421000000000"` (exactly 32 chars)
- **Tool**: Use an online hex converter to verify:
  - "HelloWorld!" in hex = `48656C6C6F576F726C6421`
  - Pad to 32 chars: `48656C6C6F576F726C64210000000000`

#### ✅ Solution 4: Wrong Key for Write
- **Problem**: Card sector write-protected with different key
- **Fix**: Verify you're using the correct key_mode and key for write operations

---

### Issue 4: DLL Loading Error

**Symptoms:**
```
Failed to initialize reader: failed to load dcrf32.dll: %1 is not a valid Win32 application
```

**Causes & Solutions:**

#### ✅ Solution 1: Wrong Architecture
- **Problem**: 32-bit DLL with 64-bit Go or vice versa
- **Check**: Your DLL architecture
- **Fix**: Rebuild for matching architecture:
  ```bash
  REM For 32-bit DLL:
  set GOARCH=386
  go build -o server.exe ./cmd/server
  
  REM For 64-bit DLL (default):
  set GOARCH=amd64
  go build -o server.exe ./cmd/server
  ```

#### ✅ Solution 2: Corrupted DLL
- **Check**: File size with `dir dcrf32.dll`
  - Should be hundreds of KB, not tiny or 0 bytes
- **Fix**: Re-copy DLL from D8/T8 SDK

#### ✅ Solution 3: Wrong DLL File
- **Check**: Is it really dcrf32.dll or dc_sdk.dll?
- **Fix**: Verify you have the correct DLL from your SDK
- **Set**: Correct DLL name in environment:
  ```bash
  set DLL_NAME=dcrf32.dll
  .\server.exe
  ```

---

### Issue 5: Server Won't Start - Port Already in Use

**Symptoms:**
```
listen tcp :8080: bind: An attempt was made to reuse a socket
```

**Solutions:**

#### ✅ Solution 1: Use Different Port
```bash
set SERVER_ADDR=:8081
.\server.exe
```

#### ✅ Solution 2: Kill Existing Process
```bash
taskkill /IM server.exe /F
```

#### ✅ Solution 3: Check What's Using Port 8080
```bash
netstat -ano | findstr :8080
```

---

## Testing Procedure

### Step 1: Verify Server is Running
```bash
curl http://localhost:8080/health
```
Expected:
```json
{"status":"ok","name":"D8/T8 Card Reader API","version":"1.0.0"}
```

### Step 2: Verify Device Connection
```bash
curl http://localhost:8080/api/v1/device/version
```
Expected:
```json
{"success":true,"data":{"version":"2.5.3"}}
```
If fails: Device not responding

### Step 3: Detect a Card
```bash
curl -X POST http://localhost:8080/api/v1/card/detect \
  -H "Content-Type: application/json" \
  -d '{"mode": 0}'
```
Expected:
```json
{"success":true,"data":{"snr_hex":"0x1A2B3C4D","snr_decimal":439410765}}
```
If error: No card or device issue

### Step 4: Read Card
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
Expected:
```json
{"success":true,"data":{"data":"000102030405060708090A0B0C0D0E0F","data_bytes":[...]}}
```
If error: Authentication or read failure

### Step 5: Halt Card
```bash
curl -X POST http://localhost:8080/api/v1/card/halt \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## Configuration Checklist

- [ ] DLL file (dcrf32.dll or dc_sdk.dll) is in project root
- [ ] Environment variable set: `set DLL_NAME=dcrf32.dll`
- [ ] Server built: `go build -o server.exe ./cmd/server`
- [ ] Server running: `.\server.exe` (or `server.exe`)
- [ ] Device connected via USB (or configured for COM port)
- [ ] Card placed on reader during testing
- [ ] Using correct card type (Mifare Classic recommended)
- [ ] Using default key (FFFFFFFFFFFF)
- [ ] Calling `/card/halt` after operations
- [ ] Not reusing IDLE mode without removing card

---

## Debug Mode - Enable Verbose Output

### Check System Logs
```bash
.\server.exe 2>&1 | tee server.log
```

This saves all output to server.log for review.

### Verify DLL is Found
```bash
where dcrf32.dll
dir dcrf32.dll
```

### Test Direct Device Commands
Check Windows Device Manager:
1. Open Device Manager
2. Look for "USB" devices
3. D8/T8 should appear as a COM port or USB device

---

## Still Having Issues?

### Gather Information:

1. **What error message exactly?**
2. **What's your card type?**
   ```bash
   REM Trying to detect?
   curl -X POST http://localhost:8080/api/v1/card/detect \
     -H "Content-Type: application/json" \
     -d '{"mode": 0}'
   ```
3. **What's the device version?**
   ```bash
   curl http://localhost:8080/api/v1/device/version
   ```
4. **What's your environment setup?**
   ```bash
   echo %READER_PORT%
   echo %READER_BAUD%
   echo %DLL_NAME%
   ```

### Reference:
- API Specification: `API_SPECIFICATION.md`
- D8/T8 Manual: `D:\PROJECTS\LTO\CORE-PLATFORM\_assets\docs\Card Reader - D8&T8 reference manual.md`
- README: `README.md`

---

*Last Updated: 2026-04-08*
