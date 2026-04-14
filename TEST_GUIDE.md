# Test Guide - D8/T8 Card Reader API

Complete testing procedures with expected responses.

---

## Prerequisites

- Server running: `.\server.exe`
- Postman installed (optional but recommended)
- cURL available (or use Postman instead)
- D8/T8 device connected via USB
- Card ready for testing

---

## Test Scenarios

### Scenario 1: Verify Server is Running

**Command:**
```bash
curl http://localhost:8080/health
```

**Expected Response (200 OK):**
```json
{
  "status": "ok",
  "name": "D8/T8 Card Reader API",
  "version": "1.0.0"
}
```

**If You See Error:**
- Server not running? Start it: `.\server.exe`
- Wrong port? Check `SERVER_ADDR` environment variable
- Connection refused? Firewall issue or server crashed

---

### Scenario 2: Verify Device Connection

**Command:**
```bash
curl http://localhost:8080/api/v1/device/version
```

**Expected Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "version": "2.5.3"
  }
}
```

**If You See Error:**
- Device not connected? Check USB cable
- DLL not found? Check `set DLL_NAME=dcrf32.dll`
- Device initialization failed? Check baud rate with `set READER_BAUD=115200`

---

### Scenario 3: Test Device Beep

**Purpose**: Confirm RF communication is working

**Command:**
```bash
curl -X POST http://localhost:8080/api/v1/device/beep ^
  -H "Content-Type: application/json" ^
  -d "{\"ms\": 100}"
```

**Expected:**
- Device makes beeping sound
- Response (200 OK):
  ```json
  {"success": true, "data": {}}
  ```

**If No Sound:**
- Device may not have speaker enabled
- Try longer duration: `"ms": 500`

---

### Scenario 4: Detect a Card (No Card Present)

**Purpose**: Test detection endpoint behavior

**Command:**
```bash
curl -X POST http://localhost:8080/api/v1/card/detect ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0}"
```

**Expected Response (500 - No Card):**
```json
{
  "success": false,
  "message": "Failed to detect card: dc_card returned -1468203008 (no card or error)",
  "code": 500
}
```

**This is expected** - no card on reader yet.

---

### Scenario 5: Detect a Card (With Card)

**Setup**: Place compatible card on reader

**Command:**
```bash
curl -X POST http://localhost:8080/api/v1/card/detect ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0}"
```

**Expected Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "snr_hex": "0x1A2B3C4D",
    "snr_decimal": 439410765
  }
}
```

**Notes:**
- SNR (Serial Number) will be different for each card
- If fails: Check card compatibility, try ALL mode: `"mode": 1`

---

### Scenario 6: Read Card Block (Complete Workflow)

**Setup**: Card on reader, run detection first

**Step 1: Detect Card**
```bash
curl -X POST http://localhost:8080/api/v1/card/detect ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0}"
```

Response:
```json
{
  "success": true,
  "data": {
    "snr_hex": "0x1A2B3C4D",
    "snr_decimal": 439410765
  }
}
```

**Step 2: Read a Block**
```bash
curl -X POST http://localhost:8080/api/v1/card/read ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0, \"sector\": 0, \"block\": 1, \"key_mode\": 0, \"key\": \"FFFFFFFFFFFF\"}"
```

**Expected (200 OK):**
```json
{
  "success": true,
  "data": {
    "data": "000102030405060708090A0B0C0D0E0F",
    "data_bytes": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15]
  }
}
```

**Step 3: Halt Card**
```bash
curl -X POST http://localhost:8080/api/v1/card/halt ^
  -H "Content-Type: application/json" ^
  -d "{}"
```

Expected:
```json
{
  "success": true,
  "data": {}
}
```

**If Read Fails:**
- Wrong key? Try different key_mode (4-6 for KEY B)
- Authentication error? Card might use custom key
- Check: TROUBLESHOOTING.md

---

### Scenario 7: Write Card Block

**Setup**: Card on reader

**Step 1: Detect Card**
```bash
curl -X POST http://localhost:8080/api/v1/card/detect ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0}"
```

**Step 2: Write Block**

Data to write (hex): `48656C6C6F576F726C64` = "HelloWorld"
Padded to 32 chars: `48656C6C6F576F726C6421000000000`

```bash
curl -X POST http://localhost:8080/api/v1/card/write ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0, \"sector\": 0, \"block\": 1, \"key_mode\": 0, \"key\": \"FFFFFFFFFFFF\", \"data\": \"48656C6C6F576F726C6421000000000\"}"
```

**Expected (200 OK):**
```json
{
  "success": true,
  "data": {}
}
```

**Step 3: Halt Card**
```bash
curl -X POST http://localhost:8080/api/v1/card/halt ^
  -H "Content-Type: application/json" ^
  -d "{}"
```

**Step 4: Verify Write by Reading**
```bash
curl -X POST http://localhost:8080/api/v1/card/detect ^
  -H "Content-Type: application/json" ^
  -d "{\"mode\": 0}"
```

Then read again to verify data was written.

---

### Scenario 8: EEPROM Operations

**Read EEPROM:**
```bash
curl -X POST http://localhost:8080/api/v1/device/eeprom/read ^
  -H "Content-Type: application/json" ^
  -d "{\"offset\": 0, \"length\": 64}"
```

**Expected (200 OK):**
```json
{
  "success": true,
  "data": {
    "data": "0000000000000000...",
    "bytes": [0, 0, 0, 0, ...]
  }
}
```

**Write EEPROM:**
```bash
curl -X POST http://localhost:8080/api/v1/device/eeprom/write ^
  -H "Content-Type: application/json" ^
  -d "{\"offset\": 0, \"data\": \"48656C6C6F\"}"
```

**Expected (200 OK):**
```json
{
  "success": true,
  "data": {}
}
```

---

## Using Postman Instead of cURL

### Import Collection:
1. Open Postman
2. Click **Import**
3. Select: `postman/D8T8-CardReader.postman_collection.json`
4. Collection imported with all endpoints

### Set Variables:
1. Click **Environments**
2. Create new environment
3. Add variables:
   - `base_url` = `http://localhost:8080`
   - `key_default` = `FFFFFFFFFFFF`

### Run Tests:
1. Select environment
2. Click endpoint
3. Click **Send**
4. See response

---

## Test Checklist

Use this to verify all components work:

### Health & Version
- [ ] GET `/health` → returns "ok"
- [ ] GET `/api/v1/device/version` → returns version

### Device Operations
- [ ] POST `/api/v1/device/beep` → device beeps
- [ ] POST `/api/v1/device/reset` → RF resets
- [ ] POST `/api/v1/device/eeprom/read` → returns data

### Card Detection
- [ ] POST `/api/v1/card/detect` (no card) → error
- [ ] POST `/api/v1/card/detect` (with card) → returns SNR

### Card Read
- [ ] Place card on reader
- [ ] POST `/api/v1/card/detect` → success
- [ ] POST `/api/v1/card/read` sector 0, block 1 → returns 16 bytes
- [ ] POST `/api/v1/card/halt` → success

### Card Write
- [ ] Place card on reader
- [ ] POST `/api/v1/card/detect` → success
- [ ] POST `/api/v1/card/write` with test data → success
- [ ] Read same block again → verify data matches

### Value Blocks
- [ ] POST `/api/v1/device/value/init` block 10 → success
- [ ] POST `/api/v1/device/value/increment` → success
- [ ] POST `/api/v1/device/value/read` → returns updated value

---

## Common Test Data

### Test Strings (for Writing)

**"HelloWorld!"**
```
Hex: 48656C6C6F576F726C6421
Full (padded): 48656C6C6F576F726C6421000000000
```

**"TEST"**
```
Hex: 54455354
Full (padded): 54455354000000000000000000000000
```

**All Zeros**
```
Hex: 00000000000000000000000000000000
```

**All Ones (255 bytes)**
```
Hex: FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF
```

---

## Performance Baseline

Typical response times (on USB connection):

| Operation | Time |
|-----------|------|
| Health check | <50ms |
| Device version | 50-100ms |
| Card detect | 50-200ms |
| Card read | 100-200ms |
| Card write | 150-300ms |
| Card halt | 50-100ms |
| Device beep | 100-500ms |

If significantly slower: Check USB connection or device load.

---

## Error Code Quick Reference

| Error Code | Meaning | Fix |
|------------|---------|-----|
| -1468203008 | No card detected | Place card on reader |
| 1 | Card operation failed | Check key, try again |
| 400 | Bad request | Invalid input format |
| 500 | Server error | Device operation failed |
| 503 | Unavailable | Device not initialized |

For detailed troubleshooting: See TROUBLESHOOTING.md

---

## Test Report Template

Use this when testing:

```
Test Date: 2026-04-08
Device: D8/T8 (dcrf32.dll)
Card: Mifare Classic 1K
Server: Running on :8080

Results:
- [ ] Health check: PASS / FAIL
- [ ] Device version: PASS / FAIL  
- [ ] Device beep: PASS / FAIL
- [ ] Card detect (no card): PASS / FAIL
- [ ] Card detect (with card): PASS / FAIL
- [ ] Card read: PASS / FAIL
- [ ] Card write: PASS / FAIL
- [ ] Card halt: PASS / FAIL
- [ ] EEPROM read: PASS / FAIL

Notes:
[observations here]

Issues Found:
[issues here]
```

---

## Next Steps

1. **Complete Scenario 1-3** - Verify server and device
2. **Run Scenario 4** - Test without card
3. **Run Scenario 5** - Test with card
4. **Run Scenario 6** - Full read workflow
5. **Run Scenario 7** - Write workflow
6. **Check all items** in Test Checklist

Once all pass ✅ - You're ready to integrate into production!

---

*Last Updated: 2026-04-08*
