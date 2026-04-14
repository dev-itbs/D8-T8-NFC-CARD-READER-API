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

func (h *DeviceHandlers) requireReader(w http.ResponseWriter) bool {
	if h.Reader == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Card reader not available. Connect the device and restart the server.",
			"code":    http.StatusServiceUnavailable,
		})
		return false
	}
	return true
}

// GetVersion godoc
//
//	@Summary		Get firmware version
//	@Description	Returns the firmware version string of the connected D8/T8 reader
//	@Tags			Device
//	@Produce		json
//	@Success		200	{object}	models.Response{data=models.VersionResponse}
//	@Failure		500	{object}	models.Response
//	@Router			/api/v1/device/version [get]
func (h *DeviceHandlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// Beep godoc
//
//	@Summary		Trigger buzzer
//	@Description	Triggers the device buzzer for the specified duration in milliseconds
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.DeviceBeepRequest	true	"Beep request"
//	@Success		200		{object}	models.Response
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/beep [post]
func (h *DeviceHandlers) Beep(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// Reset godoc
//
//	@Summary		RF field reset
//	@Description	Resets the RF field for the specified duration in milliseconds
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.DeviceResetRequest	true	"Reset request"
//	@Success		200		{object}	models.Response
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/reset [post]
func (h *DeviceHandlers) Reset(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// ReadEEPROM godoc
//
//	@Summary		Read EEPROM
//	@Description	Reads bytes from the device EEPROM at the specified offset (offset: 0–383, length: 1–384)
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.EEPROMReadRequest					true	"EEPROM read request"
//	@Success		200		{object}	models.Response{data=models.EEPROMReadResponse}
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/eeprom/read [post]
func (h *DeviceHandlers) ReadEEPROM(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// WriteEEPROM godoc
//
//	@Summary		Write EEPROM
//	@Description	Writes hex-encoded bytes to the device EEPROM at the specified offset (offset: 0–383)
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.EEPROMWriteRequest	true	"EEPROM write request"
//	@Success		200		{object}	models.Response
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/eeprom/write [post]
func (h *DeviceHandlers) WriteEEPROM(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// InitVal godoc
//
//	@Summary		Initialize value block
//	@Description	Formats a card block as a Mifare value block with the specified initial value (block: 1–63)
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.InitValRequest	true	"Init value request"
//	@Success		200		{object}	models.Response
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/value/init [post]
func (h *DeviceHandlers) InitVal(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// Increment godoc
//
//	@Summary		Increment value block
//	@Description	Adds the given value to a Mifare value block (block: 1–63)
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.IncrementRequest	true	"Increment request"
//	@Success		200		{object}	models.Response
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/value/increment [post]
func (h *DeviceHandlers) Increment(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// Decrement godoc
//
//	@Summary		Decrement value block
//	@Description	Subtracts the given value from a Mifare value block (block: 1–63)
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.DecrementRequest	true	"Decrement request"
//	@Success		200		{object}	models.Response
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/value/decrement [post]
func (h *DeviceHandlers) Decrement(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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

// ReadVal godoc
//
//	@Summary		Read value block
//	@Description	Reads the current integer value from a Mifare value block (block: 0–63)
//	@Tags			Device
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.ReadValRequest					true	"Read value request"
//	@Success		200		{object}	models.Response{data=models.ReadValResponse}
//	@Failure		400		{object}	models.Response
//	@Failure		500		{object}	models.Response
//	@Router			/api/v1/device/value/read [post]
func (h *DeviceHandlers) ReadVal(w http.ResponseWriter, r *http.Request) {
	if !h.requireReader(w) {
		return
	}
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
