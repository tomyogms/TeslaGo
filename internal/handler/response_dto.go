// Package handler — response_dto.go
//
// Response DTOs define the HTTP response contracts for all handler endpoints.
//
// Why Response DTOs?
//
// Response DTOs (Data Transfer Objects) are typed structures that represent the data
// returned by handlers. They provide:
//
//   - Compile-time type checking: field names and types are verified by the compiler
//   - Single source of truth: response schema is defined in one place
//   - IDE support: autocomplete and "find usages" work correctly
//   - Self-documentation: the struct is the API contract
//   - Consistency: field naming is enforced across all endpoints
//   - Refactoring safety: rename a field → compiler tells you everywhere it's used
//
// Without Response DTOs, you'd use gin.H (map[string]interface{}), which:
//   - Has no compile-time checks
//   - Makes typos invisible until runtime
//   - Provides no IDE autocomplete
//   - Makes tests brittle (they can't easily verify structure)
//
// See learning.md for detailed testing overhead analysis.
package handler

import (
	"time"

	"github.com/tomyogms/TeslaGo/internal/model"
)

// ──────────────────────────────────────────────────────────────────────────────
// Tesla Auth Responses
// ──────────────────────────────────────────────────────────────────────────────

// GetAuthURLResponse is returned by GET /tesla/auth/url
type GetAuthURLResponse struct {
	AuthURL string `json:"auth_url"`
	State   string `json:"state"`
}

// CallbackResponse is returned by GET /tesla/auth/callback
type CallbackResponse struct {
	Message        string    `json:"message"`
	AdminID        string    `json:"admin_id"`
	TokenExpiresAt time.Time `json:"token_expires_at"`
}

// GetVehiclesResponse is returned by GET /tesla/vehicles
type GetVehiclesResponse struct {
	Vehicles []model.TeslaVehicle `json:"vehicles"`
	Count    int                  `json:"count"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Battery Responses
// ──────────────────────────────────────────────────────────────────────────────

// GetCurrentBatteryResponse is returned by GET /tesla/vehicles/:vehicleID/battery
type GetCurrentBatteryResponse struct {
	Snapshot *model.BatterySnapshot `json:"snapshot"`
}

// GetBatteryHistoryResponse is returned by GET /tesla/vehicles/:vehicleID/battery-history
type GetBatteryHistoryResponse struct {
	Snapshots []model.BatterySnapshot `json:"snapshots"`
	Count     int                     `json:"count"`
}

// GetChargingLogsResponse is returned by GET /tesla/vehicles/:vehicleID/charging-logs
type GetChargingLogsResponse struct {
	ChargingLogs []model.ChargingLog `json:"charging_logs"`
	Count        int                 `json:"count"`
}

// PruneOldDataResponse is returned by POST /tesla/admin/prune
type PruneOldDataResponse struct {
	Message string `json:"message"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Health Response
// ──────────────────────────────────────────────────────────────────────────────

// HealthCheckResponse is returned by GET /health (already defined in model.HealthResponse)
// We don't duplicate it here since health_handler uses model.HealthResponse directly
// and it's defined in the service layer already.

// ──────────────────────────────────────────────────────────────────────────────
// Error Response
// ──────────────────────────────────────────────────────────────────────────────

// ErrorResponse represents any error response from the API
type ErrorResponse struct {
	Error string `json:"error"`
}
