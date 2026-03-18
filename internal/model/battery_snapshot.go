package model

import "time"

// BatterySnapshot captures the state of a Tesla vehicle's battery at a single
// point in time.
//
// Why snapshots?
// ──────────────
// Tesla's Owner API does not provide a history API — there is no endpoint to
// ask "what was the battery level at 3pm yesterday?". To build battery history
// we must poll the vehicle periodically and store each reading ourselves.
//
// Each time a caller hits GET /tesla/vehicles/{id}/battery we:
//  1. Fetch live data from Tesla.
//  2. Persist the result as a BatterySnapshot row.
//  3. Return the data to the caller.
//
// Over time these rows become the battery history data powering
// GET /tesla/vehicles/{id}/battery-history.
//
// 90-day retention:
//
//	Snapshots older than 90 days are removed by a cleanup job to prevent
//	unbounded table growth (see service layer).
//
// Charging state values (from Tesla's `charge_state.charging_state`):
//   - "Charging"     → actively receiving power
//   - "Complete"     → fully charged (or reached charge limit)
//   - "Disconnected" → no cable plugged in
//   - "Stopped"      → cable connected but not charging (e.g. scheduled charging)
type BatterySnapshot struct {
	// ID is the internal primary key, auto-assigned by PostgreSQL.
	ID uint `gorm:"primaryKey;autoIncrement" json:"id"`

	// VehicleID is the foreign key referencing tesla_vehicles.id (our internal PK).
	// Not to be confused with TeslaVehicle.VehicleID (Tesla's external identifier).
	// `index` creates a DB index for fast queries like "give me all snapshots for vehicle X".
	VehicleID uint `gorm:"not null;index" json:"vehicle_id"`

	// SnapshotAt is the UTC timestamp when this reading was taken.
	// Indexed because we filter and sort by time in battery-history queries.
	SnapshotAt time.Time `gorm:"not null;index" json:"snapshot_at"`

	// BatteryLevel is the current state-of-charge as a percentage (0–100).
	// Example: 80 means the battery is at 80% of its capacity.
	BatteryLevel int `gorm:"not null" json:"battery_level"`

	// BatteryRange is the estimated remaining range in miles at the current
	// charge level. Tesla calculates this based on recent driving patterns.
	BatteryRange float64 `json:"battery_range"`

	// ChargingState is the current charging activity.
	// See the type-level comment for possible values.
	ChargingState string `json:"charging_state"`

	// ChargeRate is the current charging speed in miles per hour added.
	// Only meaningful when ChargingState == "Charging". 0 otherwise.
	ChargeRate float64 `json:"charge_rate"`

	// ChargerVoltage is the voltage delivered by the charger (Volts).
	// 0 if not charging.
	ChargerVoltage int `json:"charger_voltage"`

	// ChargerActualCurrent is the actual current drawn from the charger (Amperes).
	// 0 if not charging.
	ChargerActualCurrent int `json:"charger_actual_current"`

	// ChargeLimitSOC is the target charge level set by the driver (0–100%).
	// The vehicle stops charging when BatteryLevel reaches this value.
	// Example: 80 means "only charge up to 80% to preserve battery longevity".
	ChargeLimitSOC int `json:"charge_limit_soc"`

	// TimeToFullCharge is the estimated time in hours until charging is complete.
	// Only relevant when ChargingState == "Charging". 0 otherwise.
	TimeToFullCharge float64 `json:"time_to_full_charge"`

	// ChargeEnergyAdded is the total kWh added in the current/most recent session.
	// Resets when a new charging session starts.
	ChargeEnergyAdded float64 `json:"charge_energy_added"`

	// CreatedAt is managed automatically by GORM (set on insert, never updated).
	CreatedAt time.Time `json:"created_at"`
}
