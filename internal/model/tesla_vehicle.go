package model

import "time"

// TeslaVehicle represents a single Tesla car that has been linked to a
// TeslaUser (and therefore to a TeslaGo admin).
//
// One TeslaUser can own many TeslaVehicles (a Tesla account can hold multiple cars).
// The relationship is enforced by the TeslaUserID foreign key.
//
// Vehicle IDs explained:
//   - VehicleID (our field) maps to Tesla's `id` field in the API response.
//     This is the value to use for all Owner API calls:
//     GET /api/1/vehicles/{VehicleID}/vehicle_data
//   - Tesla also returns a separate `vehicle_id` field (their internal ID for
//     the Streaming/Autopark APIs). We store the Owner API `id` here.
//
// State values reported by Tesla:
//   - "online"  → car is awake and reachable; commands can be sent immediately
//   - "asleep"  → car is sleeping to save power; must be woken before commands
//   - "offline" → car has no connectivity (no cell/Wi-Fi signal)
type TeslaVehicle struct {
	// ID is the internal primary key, auto-assigned by PostgreSQL.
	ID uint `gorm:"primaryKey;autoIncrement" json:"id"`

	// TeslaUserID is the foreign key linking this vehicle to its owner.
	// `index` creates a database index on this column for faster lookups
	// (e.g. "give me all vehicles for admin X").
	TeslaUserID uint `gorm:"not null;index" json:"tesla_user_id"`

	// VehicleID is Tesla's Owner API identifier for this vehicle.
	// Use this when constructing API paths: /api/1/vehicles/{VehicleID}/...
	// It is a large int64 because Tesla uses very large numbers (e.g. 1234567890123456789).
	VehicleID int64 `gorm:"not null" json:"vehicle_id"`

	// DisplayName is the friendly name the owner gave the car in the Tesla app.
	// e.g. "My Model 3", "Family Y". May be empty if the owner never set one.
	DisplayName string `json:"display_name"`

	// VIN is the 17-character Vehicle Identification Number, e.g. "5YJ3E1EA1LF000001".
	// It uniquely identifies the physical car worldwide.
	VIN string `json:"vin"`

	// State is the last known connectivity state of the vehicle as reported by Tesla.
	// Values: "online", "asleep", "offline". Refreshed every time we sync vehicles.
	State string `json:"state"`

	// CreatedAt and UpdatedAt are managed automatically by GORM.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
