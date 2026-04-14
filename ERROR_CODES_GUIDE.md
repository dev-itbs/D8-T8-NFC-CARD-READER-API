# Error Codes & Diagnostic Guide

## Overview

The updated API provides **detailed diagnostic error messages** to help you understand exactly what's happening and how to fix it.

Instead of a simple error message, you now get:
- **Error Code** - Numeric code from DLL
- **Suggestion** - What to try next
- **Details** - Technical explanation
- **Documentation** - Link to relevant guide
- **Retryable** - Whether it's safe to retry

---

## Example: Enhanced Error Response

**Before (Simple):**
```json
{
  "success": false,
  "message": "Failed to detect card: dc_card returned -1468203008 (no card or error)",
  "code": 500
}
```

**After (Detailed - NEW):**
```json
{
  "success": false,
  "error": "No card detected",
  "error_code": -1468203008,
  "function": "dc_card",
  "suggestion": "Place a compatible Mifare card on the reader",
  "details": "The reader's RF field is active but no valid card is in range. Ensure card is compatible (Mifare Classic 1K/4K recommended for testing).",
  "retryable": true,
  "documentation": "See TROUBLESHOOTING.md → Issue 1: dc_card returned -1468203008",
  "code": 500
}
```

---

## Error Code Reference

### Error Code 0 - Success
```
Message: Operation successful
Retryable: No
Action: None
```

### Error Code 1 or -1468203008 - No Card Detected
```
Message: No card detected
Suggestion: Place a compatible Mifare card on the reader
Details: The reader's RF field is active but no valid card is in range. 
         Ensure card is compatible (Mifare Classic 1K/4K recommended for testing).
Retryable: Yes (place card and try again)
```

**Technical Causes:**
- No card in reader range
- Card not compatible
- RF field not active
- Card in power-down mode

**Solutions:**
1. ✅ Place a card on the reader
2. ✅ Try different card (Mifare Classic 1K is most reliable)
3. ✅ Try RF reset: `POST /api/v1/device/reset {"ms": 2}`
4. ✅ Try ALL mode instead: `{"mode": 1}`
5. ✅ Check USB connection to device
6. ✅ Restart server and DLL

---

### Error Code 2 - Card Not Selected
```
Message: Card not selected
Suggestion: Call /card/detect first before read/write operations
Details: Authentication or read/write attempted without card selection.
Retryable: Yes
```

**Technical Causes:**
- Attempting read/write without detecting card first
- Card was halted or disconnected
- Device state issue

**Solution:**
```bash
# Correct sequence:
1. POST /api/v1/card/detect {"mode": 0}  ← Detect first
2. POST /api/v1/card/read { ... }         ← Then read
3. POST /api/v1/card/halt {}              ← Finally halt
```

---

### Error Code 3 - Authentication Failed
```
Message: Authentication failed
Suggestion: Verify the key is correct (default: FFFFFFFFFFFF). Try key_mode 0 for KEY A or 4 for KEY B.
Details: Card authentication failed. This typically means:
         1. Wrong key for the card
         2. Wrong key_mode (0-2 for KEY A, 4-6 for KEY B)
         3. Card sector access bits prevent authentication
Retryable: Yes
```

**Technical Causes:**
- Wrong authentication key
- Wrong key mode
- Card uses custom keys
- Sector access control bits configured
- Card already authenticated with different key

**Solutions:**

```bash
# Solution 1: Try default key with KEY A (mode 0)
curl -X POST http://localhost:8080/api/v1/card/read \
  -H "Content-Type: application/json" \
  -d '{
    "mode": 0,
    "sector": 0,
    "block": 1,
    "key_mode": 0,
    "key": "FFFFFFFFFFFF"
  }'

# Solution 2: Try KEY B (mode 4)
{
  "key_mode": 4,
  "key": "FFFFFFFFFFFF"
}

# Solution 3: If card uses custom key, provide it
{
  "key": "A1A2A3A4A5A6"  ← Your card's actual key
}
```

**Common Default Keys:**
- Mifare Classic (factory): `FFFFFFFFFFFF` (both KEY A and B)
- Some cards: `000000000000`
- If different: You need the correct key (no bypass available)

---

### Error Code 4 - Block Read Failed
```
Message: Block read failed
Suggestion: Verify sector/block range (0-15 sectors, 0-3 blocks per sector) and re-authenticate
Details: Read operation failed after authentication. 
         Check if the block address is valid and accessible.
Retryable: Yes
```

**Technical Causes:**
- Invalid block address
- Block not accessible after authentication
- Memory corruption
- Card became disconnected during operation

**Solutions:**
```bash
# Verify block address ranges:
Sector: 0-15 (16 sectors total)
Block: 0-3 within each sector (4 blocks per sector)
Block 3 is always the trailer (contains keys)

# Try reading a known-good block (sector 0, block 0):
{
  "sector": 0,
  "block": 0
}
```

---

### Error Code 5 - Block Write Failed
```
Message: Block write failed
Suggestion: Ensure block is not read-only (avoid block 3 - sector trailer). Verify write key permission.
Details: Write operation failed. Block 3 of each sector (trailer) is usually read-only.
         Also check if card has write protection on this block.
Retryable: Yes (with different block or key)
```

**Technical Causes:**
- Attempting to write block 3 (sector trailer - usually read-only)
- Block has write protection enabled
- Wrong key for write access
- Card hardware write-protected
- Memory error

**Solutions:**
```bash
# Solution 1: Don't write to block 3
# Block 3 is always the sector trailer (contains keys - protected)
# Only write to blocks 0, 1, or 2:
{
  "block": 1  ← Safe to write
}

# Solution 2: Verify data is exactly 16 bytes (32 hex chars)
{
  "data": "48656C6C6F576F726C6421000000000"  ← Exactly 32 chars
}

# Solution 3: Try a different block in the same sector
{
  "sector": 0,
  "block": 2  ← Try different block
}

# Solution 4: If card is write-protected, you can't bypass it
# The write protection is a security feature and cannot be removed
```

---

### Error Code 6 - Invalid Parameter
```
Message: Invalid parameter
Suggestion: Check parameter ranges: sector (0-15), block (0-63), key (12 hex chars)
Details: API received invalid parameters. Verify all inputs match expected format and ranges.
Retryable: No (fix input and retry)
```

**Parameter Validation Rules:**
```
Sector: 0-15
Block: 0-63
Key: Must be exactly 12 hex characters (6 bytes)
Data (write): Must be exactly 32 hex characters (16 bytes)
Mode: 0 (IDLE) or 1 (ALL)
Key Mode: 0-2 (KEY A) or 4-6 (KEY B)
```

**Example Invalid Requests:**
```json
// ❌ WRONG: Key too short
{"key": "FFFF"}

// ❌ WRONG: Block out of range
{"block": 100}

// ❌ WRONG: Data wrong length
{"data": "48656C6C6F"}

// ✅ CORRECT: All parameters valid
{
  "sector": 0,
  "block": 1,
  "key_mode": 0,
  "key": "FFFFFFFFFFFF",
  "data": "48656C6C6F576F726C6421000000000"
}
```

---

### Error Code 7 - Communication Error
```
Message: Communication error with device
Suggestion: Check USB connection, restart server, verify DLL_NAME environment variable
Details: Serial communication with the D8/T8 device failed. Device may be disconnected or unresponsive.
Retryable: Yes (after fixing connection)
```

**Technical Causes:**
- USB cable disconnected
- Device unresponsive/hung
- Wrong DLL file
- Device not initialized properly
- Serial port conflict

**Solutions:**
```bash
# 1. Verify USB connection
# → Check Device Manager for "USB" or "Serial" devices
# → Plug in device again

# 2. Verify DLL is correct
set DLL_NAME=dcrf32.dll
.\server.exe

# 3. Restart the server
Ctrl+C  # Stop server
.\server.exe  # Restart

# 4. Test device connection
curl http://localhost:8080/api/v1/device/version
# Should return version, not an error
```

---

### Error Code 8 - Device Timeout
```
Message: Device timeout
Suggestion: Device not responding. Try /device/reset, check USB connection, restart server
Details: Device operation timed out waiting for response.
Retryable: Yes
```

**Solutions:**
```bash
# 1. Reset RF field
POST /api/v1/device/reset {"ms": 2}

# 2. Check device is responding
GET /api/v1/device/version

# 3. Restart server
Ctrl+C
.\server.exe

# 4. Check USB connection
# → Unplug and replug USB cable
```

---

### Error Code 9 - Unsupported Card Type
```
Message: Unsupported card type
Suggestion: Use a compatible card: Mifare Classic 1K/4K (recommended), Mifare Ultralight, ISO14443
Details: The reader detected a card but it is not one of the supported types. 
         For best compatibility, use a Mifare Classic 1K or 4K card.
Retryable: Yes (with different card)
```

**Supported Card Types:**
✅ Mifare Classic 1K (Recommended for testing)
✅ Mifare Classic 4K
✅ Mifare Ultralight
✅ Mifare DESFire
✅ ISO14443 Type A
✅ ISO14443 Type B
✅ ISO15693

**Unsupported:**
❌ Advanced encryption cards
❌ Some proprietary cards
❌ Cards from non-standard vendors

**Solution:**
Use a standard Mifare Classic card for testing and development.

---

### Error Code 10 - Card Locked
```
Message: Card is locked/write protected
Suggestion: Card has hardware write protection. Try reading instead of writing.
Details: The card or specific blocks are write-protected and cannot be modified.
Retryable: No (cannot bypass protection)
```

**Technical Explanation:**
Some cards have permanent write protection. This is a **security feature** that cannot be bypassed. You can still **read** the card, but not **write** to protected blocks.

**What You Can Do:**
- ✅ Read protected blocks
- ❌ Write to protected blocks (not possible)
- ❌ Modify protection settings (not possible)

---

## Diagnosing Your Issue

### Step 1: Check Error Code
Look at `error_code` in the response.

### Step 2: Read Suggestion
The `suggestion` field tells you exactly what to try next.

### Step 3: Understand Details
The `details` field explains **why** it happened.

### Step 4: Check Retryable
- `"retryable": true` → Try again after fixing
- `"retryable": false` → Need to change your request

### Step 5: Consult Documentation
The `documentation` field points to detailed guides.

---

## Complete Workflow with Error Handling

```bash
# Step 1: Detect card
curl -X POST http://localhost:8080/api/v1/card/detect \
  -H "Content-Type: application/json" \
  -d '{"mode": 0}'

# Expected response with card:
{
  "success": true,
  "data": {
    "snr_hex": "0x1A2B3C4D",
    "snr_decimal": 439410765
  }
}

# If error, check error_code and suggestion
# Example error response:
{
  "success": false,
  "error": "No card detected",
  "error_code": -1468203008,
  "suggestion": "Place a compatible Mifare card on the reader",
  "details": "The reader's RF field is active but no valid card is in range...",
  "retryable": true
}
# → ACTION: Place card and try again

# Step 2: Read block (after successful detect)
curl -X POST http://localhost:8080/api/v1/card/read \
  -H "Content-Type: application/json" \
  -d '{
    "mode": 0,
    "sector": 0,
    "block": 1,
    "key_mode": 0,
    "key": "FFFFFFFFFFFF"
  }'

# Expected response:
{
  "success": true,
  "data": {
    "data": "000102030405060708090A0B0C0D0E0F",
    "data_bytes": [0, 1, 2, ...]
  }
}

# If error:
{
  "success": false,
  "error": "Authentication failed",
  "error_code": 3,
  "suggestion": "Verify the key is correct (default: FFFFFFFFFFFF)...",
  "details": "Card authentication failed...",
  "retryable": true
}
# → ACTION: Try different key_mode or key value

# Step 3: Halt card
curl -X POST http://localhost:8080/api/v1/card/halt \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## Response Format

All error responses follow this format:

```json
{
  "success": false,
  "error": "Human-readable error title",
  "error_code": 123,
  "function": "dc_function_name",
  "suggestion": "What to do next",
  "details": "Technical explanation",
  "card_type": "Card type if detected",
  "retryable": true,
  "documentation": "Link to detailed guide",
  "code": 500
}
```

**Fields:**
- `success` - Always `false` for errors
- `error` - Short error title
- `error_code` - DLL return code
- `function` - Which DLL function failed
- `suggestion` - Action to take
- `details` - Technical details
- `card_type` - What type of card (if detected)
- `retryable` - Safe to retry?
- `documentation` - Reference guide
- `code` - HTTP status code

---

## Testing Error Responses

Try these to see different error messages:

```bash
# Error 1: No card
POST /api/v1/card/detect {"mode": 0}

# Error 2: No card selected (without detect first)
POST /api/v1/card/read {"mode": 0, "sector": 0, "block": 1, ...}

# Error 3: Invalid key
POST /api/v1/card/read {"key": "INVALID"}

# Error 4: Invalid parameter
POST /api/v1/card/read {"sector": 999}

# Error 5: Write to read-only block
POST /api/v1/card/write {"block": 3, "data": "..."}
```

Each will return a detailed diagnostic error with:
- What went wrong
- Why it happened
- What to do about it

---

## Quick Reference Table

| Error Code | Issue | Retryable | Action |
|------------|-------|-----------|--------|
| 0 | Success | N/A | None |
| 1 | No card | ✅ Yes | Place card |
| 2 | Not selected | ✅ Yes | Detect first |
| 3 | Auth failed | ✅ Yes | Check key |
| 4 | Read failed | ✅ Yes | Check address |
| 5 | Write failed | ✅ Yes | Check block |
| 6 | Bad parameter | ❌ No | Fix input |
| 7 | Communication | ✅ Yes | Check USB |
| 8 | Timeout | ✅ Yes | Reset device |
| 9 | Bad card type | ✅ Yes | Different card |
| 10 | Write protected | ❌ No | Read-only |

---

## Integration Example

When integrating into your application:

```python
# Python example
response = requests.post('http://localhost:8080/api/v1/card/detect', 
                        json={"mode": 0})

if not response.json()['success']:
    error = response.json()
    print(f"Error: {error['error']}")
    print(f"Suggestion: {error['suggestion']}")
    if error['retryable']:
        # Retry logic
        print("Retrying...")
    else:
        # Fix input and try again
        print("Fix input and retry")
```

---

## Still Stuck?

1. **Check error_code** - Look it up in this guide
2. **Read suggestion** - It tells you what to try
3. **Check retryable** - Is it safe to retry?
4. **Consult documentation** - Link provided in response
5. **Review TEST_GUIDE.md** - Step-by-step scenarios

---

*Last Updated: 2026-04-08*
