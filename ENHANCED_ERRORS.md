# Enhanced Error Handling & Diagnostics

## What's Changed

Your API now provides **detailed diagnostic error messages** instead of simple error strings. Every error response includes:

- **Error Message** - What went wrong
- **Error Code** - Numeric identifier  
- **Suggestion** - What to try next
- **Details** - Technical explanation
- **Retryable** - Can you safely retry?
- **Documentation** - Link to relevant guide

---

## Before vs After

### BEFORE (Simple Error)
```json
{
  "success": false,
  "message": "Failed to detect card: dc_card returned -1468203008 (no card or error)",
  "code": 500
}
```

### AFTER (Detailed Diagnostic)
```json
{
  "success": false,
  "error": "No card detected",
  "error_code": -1468203008,
  "function": "dc_card",
  "suggestion": "Place a compatible Mifare card on the reader",
  "details": "The reader's RF field is active but no valid card is in range. Ensure card is compatible (Mifare Classic 1K/4K recommended for testing).",
  "card_type": "",
  "retryable": true,
  "documentation": "See ERROR_CODES_GUIDE.md → Error Code -1468203008 (No Card)",
  "code": 500
}
```

---

## 10 Error Codes Explained

| Code | Error | Retryable | Action |
|------|-------|-----------|--------|
| 0 | Success | N/A | None |
| 1/-1468203008 | No card detected | ✅ Yes | Place card on reader |
| 2 | Card not selected | ✅ Yes | Call /card/detect first |
| 3 | Authentication failed | ✅ Yes | Check key (default: FFFFFFFFFFFF) |
| 4 | Block read failed | ✅ Yes | Verify block address (0-63) |
| 5 | Block write failed | ✅ Yes | Avoid block 3, check permissions |
| 6 | Invalid parameter | ❌ No | Fix input format/range |
| 7 | Communication error | ✅ Yes | Check USB, restart server |
| 8 | Device timeout | ✅ Yes | Reset device, check connection |
| 9 | Unsupported card type | ✅ Yes | Use Mifare Classic 1K/4K |
| 10 | Card write-protected | ❌ No | Read-only (security feature) |

---

## Key Improvements

✅ **Actionable Suggestions** - Each error tells you what to do next  
✅ **Technical Details** - Explains why the error occurred  
✅ **Retryable Flag** - Know if you should retry or fix input  
✅ **Card Type Detection** - Identifies unsupported cards  
✅ **Error Code Mapping** - DLL codes explained in human language  
✅ **Documentation Links** - Points to relevant guides  

---

## Example: Your Error

The error you saw earlier:
```
"Failed to detect card: dc_card returned -1468203008"
```

Now returns:
```json
{
  "error": "No card detected",
  "error_code": -1468203008,
  "suggestion": "Place a compatible Mifare card on the reader",
  "details": "The reader's RF field is active but no valid card is in range...",
  "retryable": true
}
```

**Action**: Place a card and try again! ✓

---

## Implementation Details

### New Module: `internal/reader/errors.go`
- Maps 12 error codes to readable messages
- Provides diagnostic error class
- Includes card type detection framework
- Generates detailed error messages

### Updated Handlers
- `card.go` - Detect, Read, Write endpoints
- All handlers now return detailed diagnostic errors
- Proper error code extraction and formatting

### New Response Format
```json
{
  "success": false,
  "error": "Error title",
  "error_code": 123,
  "function": "dc_function",
  "suggestion": "What to do",
  "details": "Why it happened",
  "card_type": "Card type",
  "retryable": true/false,
  "documentation": "Learn more at...",
  "code": 500
}
```

---

## How to Use

1. **Get an error response** from the API
2. **Check the "suggestion"** field (what to try)
3. **Read "details"** if you want technical info
4. **Check "retryable"** flag
   - `true` → Retry after fixing
   - `false` → Fix input and try again
5. **Follow the "documentation"** link for more help

---

## Test the New Errors

Start server:
```bash
set DLL_NAME=dcrf32.dll
.\server.exe
```

Trigger error (without card):
```bash
curl -X POST http://localhost:8080/api/v1/card/detect \
  -H "Content-Type: application/json" \
  -d '{"mode": 0}'
```

Response will now show:
```json
{
  "error": "No card detected",
  "suggestion": "Place a compatible Mifare card on the reader",
  "retryable": true,
  ...
}
```

---

## Documentation

- **ERROR_CODES_GUIDE.md** - Complete error reference with solutions
- **TROUBLESHOOTING.md** - Common issues and fixes
- **TEST_GUIDE.md** - Step-by-step test scenarios

---

## Files Changed

✅ **NEW**: `internal/reader/errors.go` - Error handling module  
✅ **UPDATED**: `internal/api/handlers/card.go` - Enhanced error responses  
✅ **UPDATED**: `internal/models/response.go` - Detailed error struct  
✅ **NEW**: `ERROR_CODES_GUIDE.md` - Complete error reference  
✅ **REBUILT**: `server.exe` - With enhanced error handling  

---

## Quick Reference

Each error response tells you:
1. **What** went wrong (error message)
2. **Why** it happened (details)
3. **What to do** about it (suggestion)
4. **Can you retry** (retryable flag)
5. **Where to learn more** (documentation)

---

**Your API now provides professional-grade error diagnostics!** 🎯

See ERROR_CODES_GUIDE.md for complete error reference and solutions.
