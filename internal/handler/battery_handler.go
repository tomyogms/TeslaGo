// Package handler — battery_handler.go
//
// BatteryHandler exposes four HTTP endpoints for Phase 2:
//
//	GET /tesla/vehicles/{vehicleID}/battery
//	  → Fetches live battery data from Tesla, saves a snapshot, returns it.
//
//	GET /tesla/vehicles/{vehicleID}/battery-history?start_date=&end_date=
//	  → Returns stored battery snapshots for a time window.
//
//	GET /tesla/vehicles/{vehicleID}/charging-logs?start_date=&end_date=&limit=
//	  → Returns inferred charging sessions in a date range.
//
//	POST /tesla/admin/prune
//	  → Deletes snapshots and charging logs older than 90 days.
//
// Handler responsibilities (nothing more):
//  1. Parse and validate request parameters.
//  2. Call the service method.
//  3. Map result → HTTP response (status code + JSON body).
//
// No business logic lives here. No database imports. No Tesla API knowledge.
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gorilla/mux"

	"github.com/tomyogms/TeslaGo/internal/service"
)

// dateLayout is the expected format for start_date / end_date query params.
// RFC 3339 (ISO 8601) is the Go standard and is unambiguous across locales.
// Example: "2025-03-01T00:00:00Z"
const dateLayout = time.RFC3339

// Request DTOs for Battery endpoints.
//
// These structures define the HTTP request contracts for each endpoint.
// Validation tags ensure invalid requests are rejected at the HTTP boundary
// before reaching the service layer (fail fast, fail cheap principle).

// GetCurrentBatteryRequest represents the query and path parameters for GET /tesla/vehicles/{vehicleID}/battery.
type GetCurrentBatteryRequest struct {
	AdminID   string `form:"admin_id" validate:"required,max=255"`
	VehicleID uint   `uri:"vehicleID" validate:"required"`
}

// GetBatteryHistoryRequest represents the query and path parameters for GET /tesla/vehicles/{vehicleID}/battery-history.
type GetBatteryHistoryRequest struct {
	VehicleID uint      `uri:"vehicleID" validate:"required"`
	StartDate time.Time `form:"start_date" validate:"required"`
	EndDate   time.Time `form:"end_date" validate:"required,gtfield=StartDate"`
}

// GetChargingLogsRequest represents the query and path parameters for GET /tesla/vehicles/{vehicleID}/charging-logs.
type GetChargingLogsRequest struct {
	VehicleID uint      `uri:"vehicleID" validate:"required"`
	StartDate time.Time `form:"start_date" validate:"required"`
	EndDate   time.Time `form:"end_date" validate:"required,gtfield=StartDate"`
	Limit     int       `form:"limit" validate:"min=0,max=10000"`
}

// PruneOldDataRequest represents the request for POST /tesla/admin/prune.
// This endpoint has no parameters, but we define an empty DTO for consistency.
type PruneOldDataRequest struct {
}

// BatteryHandler holds the injected BatteryService and validator.
type BatteryHandler struct {
	// service is the business logic layer. Injected as an interface so tests
	// can substitute a mock without touching this struct.
	service service.BatteryService

	// validator is used to validate request DTOs against validation tags.
	// It is shared across all handler instances for efficiency.
	validator *validator.Validate
}

// NewBatteryHandler creates a BatteryHandler with the supplied service and validator.
func NewBatteryHandler(svc service.BatteryService, val *validator.Validate) *BatteryHandler {
	return &BatteryHandler{
		service:   svc,
		validator: val,
	}
}

// GetCurrentBattery handles GET /tesla/vehicles/{vehicleID}/battery?admin_id=<id>
//
// Fetches a live reading from Tesla, saves it as a BatterySnapshot, and returns
// the snapshot. This is the "write-through read" pattern: calling the endpoint
// both retrieves and records the current state.
//
// Path params:
//   - vehicleID: our internal tesla_vehicles.id (uint)
//
// Query params:
//   - admin_id: the admin whose Tesla account owns the vehicle
//
// Response 200:
//
//	{ "snapshot": { BatterySnapshot fields... } }
//
// Response 503 — car is asleep (Tesla returned 408).
// Response 400 — validation failed.
// Response 500 — other errors.
func (h *BatteryHandler) GetCurrentBattery(w http.ResponseWriter, r *http.Request) {
	// Step 1: Parse path parameters
	vars := mux.Vars(r)
	vehicleIDStr := vars["vehicleID"]
	var vehicleID uint
	if _, err := strconv.ParseUint(vehicleIDStr, 10, 64); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "vehicleID must be a positive integer",
		})
		return
	}
	vehicleID = uint(strconv.FormatUint(uint64(vehicleID), 10)[0])
	id, _ := strconv.ParseUint(vehicleIDStr, 10, 64)
	vehicleID = uint(id)

	// Parse query parameters
	adminID := r.URL.Query().Get("admin_id")

	// Step 2: Create and validate the request DTO
	req := GetCurrentBatteryRequest{
		AdminID:   adminID,
		VehicleID: vehicleID,
	}

	if err := h.validator.Struct(req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "admin_id is required and vehicleID must be valid",
		})
		return
	}

	// At this point, req is guaranteed valid

	snap, err := h.service.GetCurrentBattery(r.Context(), req.AdminID, req.VehicleID)
	if err != nil {
		// Detect the "car is asleep" sentinel to return 503 rather than 500.
		// A 503 (Service Unavailable) is semantically correct: the upstream
		// resource (the Tesla vehicle) is temporarily unavailable.
		if isCarAsleep(err) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "vehicle is asleep or unreachable — try again later",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to retrieve battery status",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(GetCurrentBatteryResponse{Snapshot: snap})
}

// GetBatteryHistory handles GET /tesla/vehicles/{vehicleID}/battery-history
//
// Returns time-series battery snapshots stored in our database (not a live call).
//
// Query params:
//   - start_date: RFC3339 timestamp, start of window (inclusive)
//   - end_date:   RFC3339 timestamp, end of window (inclusive)
//
// Response 200:
//
//	{ "snapshots": [...], "count": N }
//	400 — validation failed (invalid dates, end before start, etc.)
//	500 — other errors.
func (h *BatteryHandler) GetBatteryHistory(w http.ResponseWriter, r *http.Request) {
	// Step 1: Parse path parameters
	vars := mux.Vars(r)
	vehicleIDStr := vars["vehicleID"]
	id, err := strconv.ParseUint(vehicleIDStr, 10, 64)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "vehicleID must be a positive integer",
		})
		return
	}
	vehicleID := uint(id)

	// Step 2: Parse and manually convert date query parameters
	startStr := r.URL.Query().Get("start_date")
	endStr := r.URL.Query().Get("end_date")

	if startStr == "" || endStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "start_date and end_date query parameters are required (RFC3339 format)",
		})
		return
	}

	startDate, err := time.Parse(dateLayout, startStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid start_date format, expected RFC3339 e.g. 2025-01-01T00:00:00Z",
		})
		return
	}

	endDate, err := time.Parse(dateLayout, endStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid end_date format, expected RFC3339 e.g. 2025-01-31T23:59:59Z",
		})
		return
	}

	// Step 3: Create and validate the request DTO
	req := GetBatteryHistoryRequest{
		VehicleID: vehicleID,
		StartDate: startDate,
		EndDate:   endDate,
	}

	if err := h.validator.Struct(req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "end_date must be after start_date",
		})
		return
	}

	// At this point, req is guaranteed valid

	snaps, err := h.service.GetBatteryHistory(r.Context(), req.VehicleID, req.StartDate, req.EndDate)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to retrieve battery history",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(GetBatteryHistoryResponse{
		Snapshots: snaps,
		Count:     len(snaps),
	})
}

// GetChargingLogs handles GET /tesla/vehicles/{vehicleID}/charging-logs
//
// Returns inferred charging sessions from our database.
//
// Query params:
//   - start_date: RFC3339 timestamp
//   - end_date:   RFC3339 timestamp
//   - limit:      max records to return (optional, 0-10000, default 100 in service)
//
// Response 200:
//
//	{ "charging_logs": [...], "count": N }
//	400 — validation failed (invalid dates, end before start, limit out of range)
//	500 — other errors.
func (h *BatteryHandler) GetChargingLogs(w http.ResponseWriter, r *http.Request) {
	// Step 1: Parse path parameters
	vars := mux.Vars(r)
	vehicleIDStr := vars["vehicleID"]
	id, err := strconv.ParseUint(vehicleIDStr, 10, 64)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "vehicleID must be a positive integer",
		})
		return
	}
	vehicleID := uint(id)

	// Step 2: Parse and manually convert date query parameters
	startStr := r.URL.Query().Get("start_date")
	endStr := r.URL.Query().Get("end_date")

	if startStr == "" || endStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "start_date and end_date query parameters are required (RFC3339 format)",
		})
		return
	}

	startDate, err := time.Parse(dateLayout, startStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid start_date format, expected RFC3339 e.g. 2025-01-01T00:00:00Z",
		})
		return
	}

	endDate, err := time.Parse(dateLayout, endStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid end_date format, expected RFC3339 e.g. 2025-01-31T23:59:59Z",
		})
		return
	}

	// Parse optional limit param. Invalid / missing values default to 0
	// (the service applies its own default of 100).
	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = "0"
	}
	limit, _ := strconv.Atoi(limitStr)

	// Step 3: Create and validate the request DTO
	req := GetChargingLogsRequest{
		VehicleID: vehicleID,
		StartDate: startDate,
		EndDate:   endDate,
		Limit:     limit,
	}

	if err := h.validator.Struct(req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "validation failed: check dates (end after start) and limit (0-10000)",
		})
		return
	}

	// At this point, req is guaranteed valid

	logs, err := h.service.GetChargingLogs(r.Context(), req.VehicleID, req.StartDate, req.EndDate, req.Limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to retrieve charging logs",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(GetChargingLogsResponse{
		ChargingLogs: logs,
		Count:        len(logs),
	})
}

// PruneOldData handles POST /tesla/admin/prune
//
// Triggers the 90-day retention cleanup. This is an admin-only endpoint that
// deletes battery snapshots and charging logs older than 90 days.
//
// In a production system this would be protected by admin authentication
// middleware. For Phase 2 we leave auth enforcement as a TODO.
//
// Response 200:
//
//	{ "message": "old data pruned successfully" }
//	500 — other errors.
func (h *BatteryHandler) PruneOldData(w http.ResponseWriter, r *http.Request) {
	// Step 1: Create empty request DTO (for consistency, even though no params)
	var req PruneOldDataRequest

	// Step 2: Validate (will always pass since no fields to validate)
	if err := h.validator.Struct(req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid request",
		})
		return
	}

	// At this point, req is valid (trivially)

	if err := h.service.PruneOldData(r.Context()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to prune old data",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(PruneOldDataResponse{Message: "old data pruned successfully"})
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// isCarAsleep returns true if the error message indicates the Tesla vehicle
// is asleep or unreachable (Tesla returns HTTP 408 in this case).
// We use a string check here to keep things simple — in a larger codebase
// a typed sentinel error would be more robust.
func isCarAsleep(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "asleep") || contains(err.Error(), "408")
}

// contains is a tiny helper to avoid importing strings in the handler.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && len(substr) > 0 &&
			func() bool {
				for i := 0; i <= len(s)-len(substr); i++ {
					if s[i:i+len(substr)] == substr {
						return true
					}
				}
				return false
			}())
}
