package model

import "time"

// ChargingLog represents a single, complete charging session inferred from
// consecutive BatterySnapshot records.
//
// Why "inferred"?
// ───────────────
// Tesla's Owner API has no native endpoint to retrieve charging history.
// To reconstruct charging sessions we:
//  1. Poll GET /api/1/vehicles/{id}/vehicle_data periodically (on demand).
//  2. Persist each poll result as a BatterySnapshot.
//  3. Detect charging sessions by watching `charging_state` transitions:
//     Disconnected → Charging    (session starts)
//     Charging     → Complete    (session ends — charge limit reached)
//     Charging     → Stopped     (session ends — manually interrupted)
//     Charging     → Disconnected (session ends — cable unplugged mid-charge)
//  4. When a session ends, write a ChargingLog row summarising it.
//
// Session lifecycle fields:
//   - StartedAt / EndedAt bracket the session in time.
//   - EndedAt is nil (NULL in DB) while the session is still in progress.
//
// Energy accounting:
//   - EnergyAdded tracks kWh delivered (from Tesla's charge_energy_added field).
//   - StartBatteryLevel / EndBatteryLevel let callers compute SOC change.
//
// 90-day retention:
//
//	ChargingLog rows older than 90 days are pruned alongside BatterySnapshots.
type ChargingLog struct {
	// ID is the internal primary key, auto-assigned by PostgreSQL.
	ID uint `gorm:"primaryKey;autoIncrement" json:"id"`

	// VehicleID is the foreign key referencing tesla_vehicles.id (our internal PK).
	// Indexed for fast "give me all charging logs for vehicle X" queries.
	VehicleID uint `gorm:"not null;index" json:"vehicle_id"`

	// StartedAt is the UTC timestamp of the first snapshot where
	// charging_state transitioned INTO "Charging".
	StartedAt time.Time `gorm:"not null;index" json:"started_at"`

	// EndedAt is the UTC timestamp when the session concluded.
	// It is a pointer (*time.Time) so it can be NULL in the database,
	// representing a session that is still in progress.
	//
	// In Go, a nil pointer serialises to JSON `null`.
	EndedAt *time.Time `json:"ended_at"`

	// StartBatteryLevel is the battery percentage (0–100) at session start.
	StartBatteryLevel int `gorm:"not null" json:"start_battery_level"`

	// EndBatteryLevel is the battery percentage (0–100) at session end.
	// 0 if the session is still in progress.
	EndBatteryLevel int `json:"end_battery_level"`

	// EnergyAdded is the total kilowatt-hours delivered during this session.
	// Sourced from Tesla's `charge_energy_added` field.
	EnergyAdded float64 `json:"energy_added"`

	// ChargeLimit is the target SOC (%) the driver set for this session.
	// Captured from `charge_limit_soc` on the first snapshot of the session.
	ChargeLimit int `json:"charge_limit"`

	// MaxChargeRate is the peak charging speed (miles/hour) observed during
	// the session. Useful for diagnosing charger performance.
	MaxChargeRate float64 `json:"max_charge_rate"`

	// CreatedAt and UpdatedAt are managed automatically by GORM.
	// UpdatedAt is particularly important: it is refreshed each time we
	// append more snapshot data to an in-progress session.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
