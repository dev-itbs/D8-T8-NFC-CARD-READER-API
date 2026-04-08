package handlers

import (
	"encoding/json"
	"encoding/hex"
	"net/http"

	"github.com/dev-itbs/lto-reader-api/internal/models"
	"github.com/dev-itbs/lto-reader-api/internal/reader"
)

// DeviceHandlers holds dependencies for device-related handlers
type DeviceHandlers struct {
	Reader *reader.Reader
}

// NewDeviceHandlers creates a new DeviceHandlers instance
func NewDeviceHandlers(r *reader.Reader) *DeviceHandlers {
	return &DeviceHandlers{Reader: r}
}

// GetVersion returns the device firmware version
func (h *DeviceHandlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	version, err := h.Reader.GetVersion()
	if err != nil {
		respondError(w, "Failed to get version: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data: models.VersionResponse{
			Version: version,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Beep triggers the device buzzer
func (h *DeviceHandlers) Beep(w http.ResponseWriter, r *http.Request) {
	var req models.DeviceBeepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Ms < 0 {
		respondError(w, "Duration must be >= 0", http.StatusBadRequest)
		return
	}

	if err := h.Reader.Beep(req.Ms); err != nil {
		respondError(w, "Beep failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    map[string]string{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Reset performs an RF reset
func (h *DeviceHandlers) Reset(w http.ResponseWriter, r *http.Request) {
	var req models.DeviceResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.Reader.Reset(req.Ms); err != nil {
		respondError(w, "Reset failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    map[string]string{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// ReadEEPROM reads data from the device EEPROM
func (h *DeviceHandlers) ReadEEPROM(w http.ResponseWriter, r *http.Request) {
	var req models.EEPROMReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Offset < 0 || req.Offset > 383 {
		respondError(w, "Offset must be 0-383", http.StatusBadRequest)
		return
	}
	if req.Length < 1 || req.Length > 384 {
		respondError(w, "Length must be 1-384", http.StatusBadRequest)
		return
	}

	data, err := h.Reader.ReadEEPROM(req.Offset, req.Length)
	if err != nil {
		respondError(w, "EEPROM read failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data: models.EEPROMReadResponse{
			Data:  hex.EncodeToString(data),
			Bytes: data,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// WriteEEPROM writes data to the device EEPROM
func (h *DeviceHandlers) WriteEEPROM(w http.ResponseWriter, r *http.Request) {
	var req models.EEPROMWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Offset < 0 || req.Offset > 383 {
		respondError(w, "Offset must be 0-383", http.StatusBadRequest)
		return
	}

	data, err := hex.DecodeString(req.Data)
	if err != nil {
		respondError(w, "Invalid hex data: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Reader.WriteEEPROM(req.Offset, data); err != nil {
		respondError(w, "EEPROM write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    models.EEPROMWriteResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// InitVal initializes a value block
func (h *DeviceHandlers) InitVal(w http.ResponseWriter, r *http.Request) {
	var req models.InitValRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Block < 1 || req.Block > 63 {
		respondError(w, "Block must be 1-63", http.StatusBadRequest)
		return
	}

	if err := h.Reader.InitVal(req.Block, req.Value); err != nil {
		respondError(w, "InitVal failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    models.InitValResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Increment increments a value block
func (h *DeviceHandlers) Increment(w http.ResponseWriter, r *http.Request) {
	var req models.IncrementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Block < 1 || req.Block > 63 {
		respondError(w, "Block must be 1-63", http.StatusBadRequest)
		return
	}

	if err := h.Reader.Increment(req.Block, req.Value); err != nil {
		respondError(w, "Increment failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    models.IncrementResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Decrement decrements a value block
func (h *DeviceHandlers) Decrement(w http.ResponseWriter, r *http.Request) {
	var req models.DecrementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Block < 1 || req.Block > 63 {
		respondError(w, "Block must be 1-63", http.StatusBadRequest)
		return
	}

	if err := h.Reader.Decrement(req.Block, req.Value); err != nil {
		respondError(w, "Decrement failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data:    models.DecrementResponse{},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// ReadVal reads a value from a value block
func (h *DeviceHandlers) ReadVal(w http.ResponseWriter, r *http.Request) {
	var req models.ReadValRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Block < 0 || req.Block > 63 {
		respondError(w, "Block must be 0-63", http.StatusBadRequest)
		return
	}

	value, err := h.Reader.ReadVal(req.Block)
	if err != nil {
		respondError(w, "ReadVal failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := models.Response{
		Success: true,
		Data: models.ReadValResponse{
			Value: value,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
