// Package repository contains the data-access layer for TeslaGo.
//
// Responsibility of this layer:
//   - Talk to the database (via GORM).
//   - Translate between Go structs (models) and database rows.
//   - Expose a clean interface so the service layer never imports GORM directly.
//
// Clean Architecture rule: the repository layer depends on models, but NOTHING
// in the model layer knows repositories exist. Data only flows upward:
//
//	Handler → Service → Repository → Model
//	                              ↓
//	                          PostgreSQL
//
// Why use an interface?
// ─────────────────────
// The TeslaRepository interface is what the service imports, not the concrete
// struct. This means in tests we can swap the real database for a simple
// in-memory mock (see *_test.go files) without touching the service code at all.
package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tomyogms/TeslaGo/internal/model"
)

// TeslaRepository defines all the database operations the service layer needs
// for Tesla authentication and vehicle management.
//
// Rule: every method accepts a context.Context as its first argument.
// This allows the caller to cancel slow database queries (e.g. if the HTTP
// request is cancelled by the client) and prevents goroutine leaks.
type TeslaRepository interface {
	// UpsertTeslaUser creates a new TeslaUser row, or updates the token fields
	// if a row with the same admin_id already exists.
	// "Upsert" = INSERT or UPDATE — it is idempotent, safe to call repeatedly.
	UpsertTeslaUser(ctx context.Context, user *model.TeslaUser) error

	// GetTeslaUserByAdminID retrieves the TeslaUser row for a given admin.
	// Returns an error wrapping gorm.ErrRecordNotFound if no row exists.
	GetTeslaUserByAdminID(ctx context.Context, adminID string) (*model.TeslaUser, error)

	// UpsertTeslaVehicle creates or updates a vehicle row.
	// The unique key is (tesla_user_id, vehicle_id) — a vehicle can only appear
	// once per owner.
	UpsertTeslaVehicle(ctx context.Context, vehicle *model.TeslaVehicle) error

	// GetVehiclesByTeslaUserID returns all vehicles linked to a given TeslaUser.
	// Returns an empty slice (not an error) if the user has no vehicles.
	GetVehiclesByTeslaUserID(ctx context.Context, teslaUserID uint) ([]model.TeslaVehicle, error)
}

// teslaRepository is the private concrete implementation of TeslaRepository.
// It is unexported so nothing outside this package can create one directly —
// they must go through the NewTeslaRepository constructor which returns the
// interface. This enforces the dependency rule.
type teslaRepository struct {
	// db is the GORM database connection injected at construction time.
	// We never create our own connection here — it is always passed in from main.
	db *gorm.DB
}

// NewTeslaRepository creates and returns a new TeslaRepository.
// The caller provides the *gorm.DB so the repository never manages its own
// connection pool — a single shared connection is used across the whole app.
func NewTeslaRepository(db *gorm.DB) TeslaRepository {
	return &teslaRepository{db: db}
}

// UpsertTeslaUser performs an INSERT ... ON CONFLICT DO UPDATE.
//
// How GORM's OnConflict clause works:
//   - Columns: the column(s) that define uniqueness (here: admin_id).
//   - DoUpdates: if a row with the same admin_id exists, update ONLY these columns.
//     We update tokens + expiry but leave created_at untouched.
//
// Why upsert instead of separate Create/Update?
//   - Simpler: one call handles both the first link and every subsequent refresh.
//   - Atomic: the database enforces uniqueness even under concurrent requests.
func (r *teslaRepository) UpsertTeslaUser(ctx context.Context, user *model.TeslaUser) error {
	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// "admin_id" is the unique column — conflict on duplicate admin_id.
			Columns: []clause.Column{{Name: "admin_id"}},
			// On conflict, update only these columns (not id, admin_id, created_at).
			DoUpdates: clause.AssignmentColumns([]string{
				"access_token", "refresh_token", "token_expires_at", "updated_at",
			}),
		}).
		Create(user) // GORM translates this to INSERT ... ON CONFLICT DO UPDATE

	if result.Error != nil {
		return fmt.Errorf("upserting tesla user: %w", result.Error)
	}
	return nil
}

// GetTeslaUserByAdminID fetches a single TeslaUser row by admin identifier.
//
// GORM's First() method:
//   - Adds "LIMIT 1 ORDER BY id" to the query automatically.
//   - Returns gorm.ErrRecordNotFound if no row matches — the service layer
//     can check for this to return a 404 instead of a 500.
func (r *teslaRepository) GetTeslaUserByAdminID(ctx context.Context, adminID string) (*model.TeslaUser, error) {
	var user model.TeslaUser
	// WHERE admin_id = ? LIMIT 1
	result := r.db.WithContext(ctx).Where("admin_id = ?", adminID).First(&user)
	if result.Error != nil {
		return nil, fmt.Errorf("getting tesla user by admin_id: %w", result.Error)
	}
	return &user, nil
}

// UpsertTeslaVehicle performs an INSERT ... ON CONFLICT DO UPDATE for a vehicle.
//
// The unique constraint is on the combination of (tesla_user_id, vehicle_id):
//   - Same admin, same Tesla vehicle ID → update display_name, vin, state.
//   - New vehicle → insert a new row.
//
// This is called after every successful OAuth callback to keep vehicle data fresh.
func (r *teslaRepository) UpsertTeslaVehicle(ctx context.Context, vehicle *model.TeslaVehicle) error {
	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// Conflict when the same vehicle already exists for the same user.
			Columns: []clause.Column{
				{Name: "tesla_user_id"},
				{Name: "vehicle_id"},
			},
			// On conflict, refresh the mutable fields (name/state can change).
			DoUpdates: clause.AssignmentColumns([]string{
				"display_name", "vin", "state", "updated_at",
			}),
		}).
		Create(vehicle)

	if result.Error != nil {
		return fmt.Errorf("upserting tesla vehicle: %w", result.Error)
	}
	return nil
}

// GetVehiclesByTeslaUserID returns all vehicles belonging to a TeslaUser.
//
// GORM's Find() method (vs First()):
//   - Find() returns ALL matching rows as a slice. It does NOT error if zero
//     rows are found — it simply returns an empty slice.
//   - First() returns exactly one row and errors on zero rows.
//   - We use Find() here because zero vehicles is a valid, expected state.
func (r *teslaRepository) GetVehiclesByTeslaUserID(ctx context.Context, teslaUserID uint) ([]model.TeslaVehicle, error) {
	var vehicles []model.TeslaVehicle
	// WHERE tesla_user_id = ?
	result := r.db.WithContext(ctx).Where("tesla_user_id = ?", teslaUserID).Find(&vehicles)
	if result.Error != nil {
		return nil, fmt.Errorf("getting vehicles by tesla_user_id: %w", result.Error)
	}
	return vehicles, nil
}
