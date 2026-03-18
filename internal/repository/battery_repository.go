// Package repository — battery_repository.go
//
// This file defines the BatteryRepository interface and its GORM implementation.
// It handles persistence for two models: BatterySnapshot and ChargingLog.
//
// Why one repository for two models?
// ────────────────────────────────────
// BatterySnapshot and ChargingLog are tightly coupled — charging sessions are
// derived directly from snapshots. Keeping them in one repository avoids
// cross-repository dependencies and makes the service layer's data access
// simpler: one injection, all the data operations it needs.
//
// Clean Architecture reminder:
//
//	Service → BatteryRepository interface (defined here)
//	           ↓
//	        batteryRepository struct (GORM implementation, also here)
//	           ↓
//	        PostgreSQL
//
// The service layer imports only the interface. The GORM struct is invisible
// to everything except the router's wiring code.
package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/tomyogms/TeslaGo/internal/model"
)

// BatteryRepository defines all database operations the battery and charging
// services need. All methods follow the same conventions as TeslaRepository:
//   - First argument is always context.Context for cancellation support.
//   - Errors are wrapped with fmt.Errorf for context-rich stack traces.
type BatteryRepository interface {
	// ── BatterySnapshot operations ────────────────────────────────────────────

	// SaveSnapshot inserts a new BatterySnapshot row.
	// Called every time we successfully poll vehicle_data from Tesla.
	SaveSnapshot(ctx context.Context, snap *model.BatterySnapshot) error

	// GetLatestSnapshot returns the most recent BatterySnapshot for the given
	// vehicle (ordered by snapshot_at DESC LIMIT 1).
	// Returns an error wrapping gorm.ErrRecordNotFound if no snapshots exist.
	GetLatestSnapshot(ctx context.Context, vehicleID uint) (*model.BatterySnapshot, error)

	// GetSnapshotsByVehicleAndTimeRange returns all snapshots for a vehicle
	// within [from, to] (inclusive), ordered by snapshot_at ASC.
	// Returns an empty slice (no error) if none match.
	GetSnapshotsByVehicleAndTimeRange(ctx context.Context, vehicleID uint, from, to time.Time) ([]model.BatterySnapshot, error)

	// DeleteSnapshotsOlderThan removes all snapshots with snapshot_at before
	// the given cutoff. Used by the 90-day retention cleanup job.
	DeleteSnapshotsOlderThan(ctx context.Context, cutoff time.Time) error

	// ── ChargingLog operations ────────────────────────────────────────────────

	// SaveChargingLog inserts a new ChargingLog row (start of a new session).
	SaveChargingLog(ctx context.Context, log *model.ChargingLog) error

	// UpdateChargingLog updates an existing ChargingLog row (session end details).
	// Only EndedAt, EndBatteryLevel, EnergyAdded, and MaxChargeRate are updated.
	UpdateChargingLog(ctx context.Context, log *model.ChargingLog) error

	// GetChargingLogsByVehicleAndTimeRange returns all charging sessions for a
	// vehicle where started_at falls within [from, to], ordered by started_at DESC.
	GetChargingLogsByVehicleAndTimeRange(ctx context.Context, vehicleID uint, from, to time.Time, limit int) ([]model.ChargingLog, error)

	// GetOpenChargingLog returns the most recent charging session for a vehicle
	// that has not yet ended (ended_at IS NULL). Returns nil (no error) if there
	// is no open session.
	GetOpenChargingLog(ctx context.Context, vehicleID uint) (*model.ChargingLog, error)

	// DeleteChargingLogsOlderThan removes all charging logs started before cutoff.
	DeleteChargingLogsOlderThan(ctx context.Context, cutoff time.Time) error
}

// batteryRepository is the private GORM implementation of BatteryRepository.
type batteryRepository struct {
	// db is the shared GORM connection injected at construction time.
	db *gorm.DB
}

// NewBatteryRepository creates and returns a BatteryRepository backed by GORM.
// Pass in the same *gorm.DB shared across all repositories — do not create a
// new connection here.
func NewBatteryRepository(db *gorm.DB) BatteryRepository {
	return &batteryRepository{db: db}
}

// ─────────────────────────────────────────────────────────────────────────────
// BatterySnapshot implementations
// ─────────────────────────────────────────────────────────────────────────────

// SaveSnapshot inserts a fresh BatterySnapshot row.
//
// We use a plain Create (not upsert) because every snapshot is a new,
// unique data point. There is no "update existing snapshot" operation.
func (r *batteryRepository) SaveSnapshot(ctx context.Context, snap *model.BatterySnapshot) error {
	result := r.db.WithContext(ctx).Create(snap)
	if result.Error != nil {
		return fmt.Errorf("saving battery snapshot: %w", result.Error)
	}
	return nil
}

// GetLatestSnapshot fetches the single most recent snapshot for a vehicle.
//
// GORM's First() with an explicit ORDER BY gives us the newest row.
// ORDER BY snapshot_at DESC is redundant with the composite index but makes
// the intent explicit and the query fast regardless of index usage decisions.
func (r *batteryRepository) GetLatestSnapshot(ctx context.Context, vehicleID uint) (*model.BatterySnapshot, error) {
	var snap model.BatterySnapshot
	result := r.db.WithContext(ctx).
		Where("vehicle_id = ?", vehicleID).
		Order("snapshot_at DESC").
		First(&snap)
	if result.Error != nil {
		return nil, fmt.Errorf("getting latest battery snapshot: %w", result.Error)
	}
	return &snap, nil
}

// GetSnapshotsByVehicleAndTimeRange fetches all snapshots in a time window.
//
// The BETWEEN equivalent in GORM is two Where conditions:
//
//	WHERE vehicle_id = ? AND snapshot_at >= ? AND snapshot_at <= ?
//
// Results are ordered chronologically (ASC) so the caller gets a time-series
// ready to plot or analyse.
func (r *batteryRepository) GetSnapshotsByVehicleAndTimeRange(ctx context.Context, vehicleID uint, from, to time.Time) ([]model.BatterySnapshot, error) {
	var snaps []model.BatterySnapshot
	result := r.db.WithContext(ctx).
		Where("vehicle_id = ? AND snapshot_at >= ? AND snapshot_at <= ?", vehicleID, from, to).
		Order("snapshot_at ASC").
		Find(&snaps)
	if result.Error != nil {
		return nil, fmt.Errorf("getting battery snapshots by time range: %w", result.Error)
	}
	return snaps, nil
}

// DeleteSnapshotsOlderThan bulk-deletes all rows before the cutoff timestamp.
//
// GORM's Delete with a Where clause translates to:
//
//	DELETE FROM battery_snapshots WHERE snapshot_at < ?
//
// This is called periodically by the retention cleanup logic in the service.
func (r *batteryRepository) DeleteSnapshotsOlderThan(ctx context.Context, cutoff time.Time) error {
	result := r.db.WithContext(ctx).
		Where("snapshot_at < ?", cutoff).
		Delete(&model.BatterySnapshot{})
	if result.Error != nil {
		return fmt.Errorf("deleting old battery snapshots: %w", result.Error)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ChargingLog implementations
// ─────────────────────────────────────────────────────────────────────────────

// SaveChargingLog inserts a new ChargingLog row for a newly detected session.
// At insert time EndedAt is nil — it will be filled when the session closes.
func (r *batteryRepository) SaveChargingLog(ctx context.Context, log *model.ChargingLog) error {
	result := r.db.WithContext(ctx).Create(log)
	if result.Error != nil {
		return fmt.Errorf("saving charging log: %w", result.Error)
	}
	return nil
}

// UpdateChargingLog persists the closing details of a charging session.
//
// GORM's Save() performs a full-record UPDATE. We only update the fields that
// change when a session ends; the rest remain as originally inserted.
//
// We use Select to be explicit about which columns we want to update, which
// avoids accidentally zeroing out columns if the struct is partially populated.
func (r *batteryRepository) UpdateChargingLog(ctx context.Context, log *model.ChargingLog) error {
	result := r.db.WithContext(ctx).
		Model(log).
		Select("ended_at", "end_battery_level", "energy_added", "max_charge_rate", "updated_at").
		Updates(log)
	if result.Error != nil {
		return fmt.Errorf("updating charging log: %w", result.Error)
	}
	return nil
}

// GetChargingLogsByVehicleAndTimeRange fetches completed (and in-progress)
// charging sessions within a date range, newest first.
//
// `limit` prevents unbounded result sets — callers pass this from the
// query parameter (default 100, max enforced in the service layer).
func (r *batteryRepository) GetChargingLogsByVehicleAndTimeRange(ctx context.Context, vehicleID uint, from, to time.Time, limit int) ([]model.ChargingLog, error) {
	var logs []model.ChargingLog
	result := r.db.WithContext(ctx).
		Where("vehicle_id = ? AND started_at >= ? AND started_at <= ?", vehicleID, from, to).
		Order("started_at DESC").
		Limit(limit).
		Find(&logs)
	if result.Error != nil {
		return nil, fmt.Errorf("getting charging logs by time range: %w", result.Error)
	}
	return logs, nil
}

// GetOpenChargingLog finds the most recent charging session for a vehicle that
// has not ended yet (ended_at IS NULL).
//
// Why do we need this?
//
//	When a new battery snapshot arrives and the car is still "Charging", we
//	need to update the in-progress session's max_charge_rate and energy_added
//	without creating a duplicate row. GetOpenChargingLog lets us find that row.
//
// Returns nil, nil (not an error) when there is no open session — the caller
// treats this as "start a new session".
func (r *batteryRepository) GetOpenChargingLog(ctx context.Context, vehicleID uint) (*model.ChargingLog, error) {
	var log model.ChargingLog
	result := r.db.WithContext(ctx).
		Where("vehicle_id = ? AND ended_at IS NULL", vehicleID).
		Order("started_at DESC").
		First(&log)

	// gorm.ErrRecordNotFound is a normal condition here (no open session).
	// Return nil, nil so the caller can distinguish "not found" from a real error.
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil //nolint:nilnil // intentional: "no open session" is not an error
		}
		return nil, fmt.Errorf("getting open charging log: %w", result.Error)
	}
	return &log, nil
}

// DeleteChargingLogsOlderThan removes sessions that started before the cutoff.
// Mirrors DeleteSnapshotsOlderThan — both are called together by the cleanup job.
func (r *batteryRepository) DeleteChargingLogsOlderThan(ctx context.Context, cutoff time.Time) error {
	result := r.db.WithContext(ctx).
		Where("started_at < ?", cutoff).
		Delete(&model.ChargingLog{})
	if result.Error != nil {
		return fmt.Errorf("deleting old charging logs: %w", result.Error)
	}
	return nil
}
