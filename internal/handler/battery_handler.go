// Package handler — battery_handler.go
//
// BatteryHandler exposes four HTTP endpoints for Phase 2:
//
//	GET /tesla/vehicles/:vehicleID/battery
//	  → Fetches live battery data from Tesla, saves a snapshot, returns it.
//
//	GET /tesla/vehicles/:vehicleID/battery-history?admin_id=&start_date=&end_date=
//	  → Returns stored battery snapshots for a time window.
//
//	GET /tesla/vehicles/:vehicleID/charging-logs?admin_id=&start_date=&end_date=&limit=
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
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tomyogms/TeslaGo/internal/service"
)

// dateLayout is the expected format for start_date / end_date query params.
// RFC 3339 (ISO 8601) is the Go standard and is unambiguous across locales.
// Example: "2025-03-01T00:00:00Z"
const dateLayout = time.RFC3339

// BatteryHandler holds the injected BatteryService.
type BatteryHandler struct {
	// service is the business logic layer. Injected as an interface so tests
	// can substitute a mock without touching this struct.
	service service.BatteryService
}

// NewBatteryHandler creates a BatteryHandler with the supplied service.
func NewBatteryHandler(svc service.BatteryService) *BatteryHandler {
	return &BatteryHandler{service: svc}
}

// GetCurrentBattery handles GET /tesla/vehicles/:vehicleID/battery?admin_id=<id>
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
// Response 500 — other errors.
func (h *BatteryHandler) GetCurrentBattery(c *gin.Context) {
	adminID := c.Query("admin_id")
	if adminID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin_id query parameter is required"})
		return
	}

	vehicleID, ok := parseVehicleID(c)
	if !ok {
		return // parseVehicleID already wrote the error response
	}

	snap, err := h.service.GetCurrentBattery(c.Request.Context(), adminID, vehicleID)
	if err != nil {
		// Detect the "car is asleep" sentinel to return 503 rather than 500.
		// A 503 (Service Unavailable) is semantically correct: the upstream
		// resource (the Tesla vehicle) is temporarily unavailable.
		if isCarAsleep(err) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "vehicle is asleep or unreachable — try again later",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve battery status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"snapshot": snap})
}

// GetBatteryHistory handles GET /tesla/vehicles/:vehicleID/battery-history
//
// Returns time-series battery snapshots stored in our database (not a live call).
//
// Query params:
//   - admin_id:   (not used for data fetching but validated for consistency)
//   - start_date: RFC3339 timestamp, start of window (inclusive)
//   - end_date:   RFC3339 timestamp, end of window (inclusive)
//
// Response 200:
//
//	{ "snapshots": [...], "count": N }
func (h *BatteryHandler) GetBatteryHistory(c *gin.Context) {
	vehicleID, ok := parseVehicleID(c)
	if !ok {
		return
	}

	from, to, ok := parseDateRange(c)
	if !ok {
		return
	}

	snaps, err := h.service.GetBatteryHistory(c.Request.Context(), vehicleID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve battery history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"snapshots": snaps,
		"count":     len(snaps),
	})
}

// GetChargingLogs handles GET /tesla/vehicles/:vehicleID/charging-logs
//
// Returns inferred charging sessions from our database.
//
// Query params:
//   - start_date: RFC3339 timestamp
//   - end_date:   RFC3339 timestamp
//   - limit:      max records to return (default 100, enforced in service)
//
// Response 200:
//
//	{ "charging_logs": [...], "count": N }
func (h *BatteryHandler) GetChargingLogs(c *gin.Context) {
	vehicleID, ok := parseVehicleID(c)
	if !ok {
		return
	}

	from, to, ok := parseDateRange(c)
	if !ok {
		return
	}

	// Parse optional limit param. Invalid / missing values fall back to 0
	// (the service applies its own default of 100).
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "0"))

	logs, err := h.service.GetChargingLogs(c.Request.Context(), vehicleID, from, to, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve charging logs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"charging_logs": logs,
		"count":         len(logs),
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
func (h *BatteryHandler) PruneOldData(c *gin.Context) {
	if err := h.service.PruneOldData(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prune old data"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "old data pruned successfully"})
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// parseVehicleID reads :vehicleID from the Gin path params and converts it to
// a uint. On failure it writes a 400 response and returns false.
func parseVehicleID(c *gin.Context) (uint, bool) {
	raw := c.Param("vehicleID")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vehicleID must be a positive integer"})
		return 0, false
	}
	return uint(id), true
}

// parseDateRange reads start_date and end_date query params (RFC3339) and
// returns the parsed time.Time values. On failure it writes a 400 response
// and returns false.
func parseDateRange(c *gin.Context) (from, to time.Time, ok bool) {
	startStr := c.Query("start_date")
	endStr := c.Query("end_date")

	if startStr == "" || endStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date and end_date query parameters are required (RFC3339 format)"})
		return time.Time{}, time.Time{}, false
	}

	var err error
	from, err = time.Parse(dateLayout, startStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start_date format, expected RFC3339 e.g. 2025-01-01T00:00:00Z"})
		return time.Time{}, time.Time{}, false
	}

	to, err = time.Parse(dateLayout, endStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end_date format, expected RFC3339 e.g. 2025-01-31T23:59:59Z"})
		return time.Time{}, time.Time{}, false
	}

	if to.Before(from) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_date must be after start_date"})
		return time.Time{}, time.Time{}, false
	}

	return from, to, true
}

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
