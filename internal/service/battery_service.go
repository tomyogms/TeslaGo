// Package service — battery_service.go
//
// BatteryService is the heart of Phase 2. It orchestrates:
//   - Fetching live battery data from the Tesla API (via TeslaAuthService for token management)
//   - Persisting each reading as a BatterySnapshot
//   - Detecting charging session transitions and maintaining ChargingLog records
//   - Serving historical battery snapshots and charging logs from the database
//   - Pruning data older than 90 days
//
// ──────────────────────────────────────────────────────────────────────────────
// Charging Session Inference — How it works
// ──────────────────────────────────────────────────────────────────────────────
//
// Tesla's API has no native charging history endpoint. We infer sessions by
// watching the `charging_state` field across consecutive snapshots:
//
//	Snapshot N-1        Snapshot N          Action
//	─────────────────   ─────────────────   ──────────────────────────────────
//	Disconnected        Charging            → Open a new ChargingLog
//	Stopped             Charging            → Open a new ChargingLog
//	Charging            Charging            → Update open session (max rate, energy)
//	Charging            Complete            → Close the open ChargingLog
//	Charging            Stopped             → Close the open ChargingLog
//	Charging            Disconnected        → Close the open ChargingLog
//	Complete/Disconnected/Stopped           → No action needed
//
// The service reads the most recent snapshot from the DB to determine the
// "previous state", then compares it to the new reading.
//
// ──────────────────────────────────────────────────────────────────────────────
// 90-day retention
// ──────────────────────────────────────────────────────────────────────────────
//
// PruneOldData() deletes snapshots and logs older than 90 days. It should be
// called periodically (e.g. from a cron job or scheduled HTTP endpoint).
// In Phase 2 it is exposed via an internal admin endpoint; background workers
// are a Phase 3 concern.
package service

import (
	"context"
	"fmt"
	"math"
	"time"

	extTesla "github.com/tomyogms/TeslaGo/external/tesla"
	"github.com/tomyogms/TeslaGo/internal/model"
	"github.com/tomyogms/TeslaGo/internal/repository"
)

// retentionDays is the number of days battery data is kept before deletion.
const retentionDays = 90

// defaultQueryLimit is used when the caller does not specify a limit.
// It caps the number of charging logs or snapshots returned in one response.
const defaultQueryLimit = 100

// BatteryService is the interface handlers depend on.
// Defining an interface means handlers never import the concrete struct — they
// only see this contract. Tests can inject a mock that satisfies the interface.
type BatteryService interface {
	// GetCurrentBattery fetches a live battery reading for the vehicle, persists
	// it as a BatterySnapshot, updates ChargingLog records, and returns the snapshot.
	//
	// vehicleID is our internal tesla_vehicles.id (uint), not Tesla's external ID.
	// adminID is needed to look up the valid access token via TeslaAuthService.
	GetCurrentBattery(ctx context.Context, adminID string, vehicleID uint) (*model.BatterySnapshot, error)

	// GetBatteryHistory returns time-series battery snapshots for a vehicle.
	// from / to define the inclusive time window.
	GetBatteryHistory(ctx context.Context, vehicleID uint, from, to time.Time) ([]model.BatterySnapshot, error)

	// GetChargingLogs returns completed (and in-progress) charging sessions for
	// a vehicle within the given time window, newest first.
	// limit caps the number of records; pass 0 to use the default (100).
	GetChargingLogs(ctx context.Context, vehicleID uint, from, to time.Time, limit int) ([]model.ChargingLog, error)

	// PruneOldData removes snapshots and charging logs older than 90 days.
	// Returns the combined error if either deletion fails.
	PruneOldData(ctx context.Context) error
}

// TeslaVehicleDataClient is the subset of external/tesla.Client that
// BatteryService uses. Defining it here (consumer-side) is the Go idiom:
// "accept interfaces, return structs". It makes the service testable without
// real HTTP calls by letting tests inject a mock.
type TeslaVehicleDataClient interface {
	GetVehicleData(accessToken string, vehicleID int64) (*extTesla.VehicleData, error)
}

// batteryService is the private concrete implementation of BatteryService.
// All dependencies are injected via NewBatteryService — never created internally.
type batteryService struct {
	// batteryRepo handles all DB reads and writes for snapshots and sessions.
	batteryRepo repository.BatteryRepository

	// teslaRepo is needed to look up a vehicle's Tesla-side vehicle_id (int64)
	// given our internal vehicle_id (uint). We store Tesla's external ID in
	// tesla_vehicles.vehicle_id.
	teslaRepo repository.TeslaRepository

	// authService provides GetValidAccessToken — the single source of truth for
	// a ready-to-use, non-expired Tesla Bearer token.
	authService TeslaAuthService

	// teslaClient wraps the Tesla Owner API HTTP calls.
	teslaClient TeslaVehicleDataClient
}

// NewBatteryService creates a BatteryService with all its dependencies.
//
// Parameters:
//   - batteryRepo: BatteryRepository for snapshot + charging log persistence
//   - teslaRepo:   TeslaRepository to look up vehicle details (Tesla external ID)
//   - authService: TeslaAuthService to obtain a valid access token per admin
//   - teslaClient: HTTP client for GET /api/1/vehicles/{id}/vehicle_data
func NewBatteryService(
	batteryRepo repository.BatteryRepository,
	teslaRepo repository.TeslaRepository,
	authService TeslaAuthService,
	teslaClient TeslaVehicleDataClient,
) BatteryService {
	return &batteryService{
		batteryRepo: batteryRepo,
		teslaRepo:   teslaRepo,
		authService: authService,
		teslaClient: teslaClient,
	}
}

// GetCurrentBattery is the main Phase 2 operation. It:
//  1. Looks up the vehicle to obtain Tesla's external vehicle_id (int64).
//  2. Gets a valid access token via authService.
//  3. Calls Tesla API for live vehicle_data.
//  4. Persists the result as a BatterySnapshot.
//  5. Infers charging session state changes and updates ChargingLog accordingly.
//  6. Returns the newly saved snapshot.
func (s *batteryService) GetCurrentBattery(ctx context.Context, adminID string, vehicleID uint) (*model.BatterySnapshot, error) {
	// Step 1: Look up the vehicle row so we can get Tesla's external vehicleID.
	// We need the int64 Tesla ID to construct the API URL.
	teslaUser, err := s.teslaRepo.GetTeslaUserByAdminID(ctx, adminID)
	if err != nil {
		return nil, fmt.Errorf("looking up tesla user: %w", err)
	}

	vehicles, err := s.teslaRepo.GetVehiclesByTeslaUserID(ctx, teslaUser.ID)
	if err != nil {
		return nil, fmt.Errorf("looking up vehicles: %w", err)
	}

	// Find the specific vehicle by our internal ID.
	var teslaVehicleID int64
	found := false
	for _, v := range vehicles {
		if v.ID == vehicleID {
			teslaVehicleID = v.VehicleID // Tesla's external Owner API ID
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("vehicle %d not found for admin %s", vehicleID, adminID)
	}

	// Step 2: Get a valid (non-expired) access token.
	// authService handles refresh automatically if the token is near expiry.
	accessToken, err := s.authService.GetValidAccessToken(ctx, adminID)
	if err != nil {
		return nil, fmt.Errorf("getting access token: %w", err)
	}

	// Step 3: Call the Tesla Owner API.
	// If the car is asleep, Tesla returns 408 and GetVehicleData wraps it with
	// "vehicle is asleep or unreachable". We propagate that error up so the
	// handler can return 503 to the client.
	vehicleData, err := s.teslaClient.GetVehicleData(accessToken, teslaVehicleID)
	if err != nil {
		return nil, fmt.Errorf("fetching vehicle data from tesla: %w", err)
	}

	// Step 4: Map the Tesla API response into our BatterySnapshot model.
	cs := vehicleData.ChargeState
	snap := &model.BatterySnapshot{
		VehicleID:            vehicleID,
		SnapshotAt:           time.Now().UTC(),
		BatteryLevel:         cs.BatteryLevel,
		BatteryRange:         cs.BatteryRange,
		ChargingState:        cs.ChargingState,
		ChargeRate:           cs.ChargeRate,
		ChargerVoltage:       cs.ChargerVoltage,
		ChargerActualCurrent: cs.ChargerActualCurrent,
		ChargeLimitSOC:       cs.ChargeLimitSOC,
		TimeToFullCharge:     cs.TimeToFullCharge,
		ChargeEnergyAdded:    cs.ChargeEnergyAdded,
	}

	// Persist the snapshot. If this fails the flow stops — we do not want to
	// update charging logs without having saved the underlying snapshot first.
	if err := s.batteryRepo.SaveSnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("saving battery snapshot: %w", err)
	}

	// Step 5: Infer charging session changes based on this new snapshot.
	// This is best-effort — a failure here does not invalidate the snapshot we
	// just saved. The snapshot data is always correct; the session detection is
	// a secondary derived operation.
	_ = s.updateChargingSession(ctx, snap)

	return snap, nil
}

// GetBatteryHistory returns stored snapshots for a vehicle in a time window.
func (s *batteryService) GetBatteryHistory(ctx context.Context, vehicleID uint, from, to time.Time) ([]model.BatterySnapshot, error) {
	snaps, err := s.batteryRepo.GetSnapshotsByVehicleAndTimeRange(ctx, vehicleID, from, to)
	if err != nil {
		return nil, fmt.Errorf("getting battery history: %w", err)
	}
	return snaps, nil
}

// GetChargingLogs returns charging sessions in a time window.
// If limit is 0 or negative it falls back to defaultQueryLimit.
func (s *batteryService) GetChargingLogs(ctx context.Context, vehicleID uint, from, to time.Time, limit int) ([]model.ChargingLog, error) {
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	logs, err := s.batteryRepo.GetChargingLogsByVehicleAndTimeRange(ctx, vehicleID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("getting charging logs: %w", err)
	}
	return logs, nil
}

// PruneOldData removes all battery snapshots and charging logs older than
// retentionDays (90 days). Both deletions are attempted independently so a
// failure in one does not prevent the other.
func (s *batteryService) PruneOldData(ctx context.Context) error {
	// cutoff is "now minus 90 days" in UTC.
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	var pruneErr error

	if err := s.batteryRepo.DeleteSnapshotsOlderThan(ctx, cutoff); err != nil {
		pruneErr = fmt.Errorf("pruning battery snapshots: %w", err)
	}

	if err := s.batteryRepo.DeleteChargingLogsOlderThan(ctx, cutoff); err != nil {
		if pruneErr != nil {
			// Both failed — combine the errors for visibility.
			pruneErr = fmt.Errorf("%w; pruning charging logs: %v", pruneErr, err)
		} else {
			pruneErr = fmt.Errorf("pruning charging logs: %w", err)
		}
	}

	return pruneErr
}

// ─────────────────────────────────────────────────────────────────────────────
// Private: Charging Session Inference
// ─────────────────────────────────────────────────────────────────────────────

// updateChargingSession compares the new snapshot's charging_state to the
// previous state (from the DB) and updates ChargingLog records accordingly.
//
// This implements the state machine described in the package-level comment.
//
// It is a private method because no one outside the service should trigger it
// directly — it is always a side-effect of saving a new snapshot.
func (s *batteryService) updateChargingSession(ctx context.Context, snap *model.BatterySnapshot) error {
	newState := snap.ChargingState

	// ── Is there an open (in-progress) charging session? ──────────────────────
	openLog, err := s.batteryRepo.GetOpenChargingLog(ctx, snap.VehicleID)
	if err != nil {
		return fmt.Errorf("checking for open charging session: %w", err)
	}

	switch {
	case newState == "Charging" && openLog == nil:
		// Transition INTO charging with no open session → start a new session.
		return s.startChargingSession(ctx, snap)

	case newState == "Charging" && openLog != nil:
		// Still charging → update the in-progress session metrics.
		return s.updateOngoingSession(ctx, openLog, snap)

	case newState != "Charging" && openLog != nil:
		// Transitioned OUT OF charging → close the open session.
		return s.closeChargingSession(ctx, openLog, snap)

	default:
		// Not charging and no open session — nothing to do.
		return nil
	}
}

// startChargingSession creates a new ChargingLog row when we detect the
// vehicle has started charging (previous state was not "Charging").
func (s *batteryService) startChargingSession(ctx context.Context, snap *model.BatterySnapshot) error {
	log := &model.ChargingLog{
		VehicleID:         snap.VehicleID,
		StartedAt:         snap.SnapshotAt,
		StartBatteryLevel: snap.BatteryLevel,
		ChargeLimit:       snap.ChargeLimitSOC,
		MaxChargeRate:     snap.ChargeRate,
		EnergyAdded:       snap.ChargeEnergyAdded,
		// EndedAt is nil (NULL) — session is still open.
		// EndBatteryLevel stays 0 until the session closes.
	}
	if err := s.batteryRepo.SaveChargingLog(ctx, log); err != nil {
		return fmt.Errorf("starting charging session: %w", err)
	}
	return nil
}

// updateOngoingSession refreshes the in-progress session's running metrics
// without closing it. We track the maximum charge rate seen and the latest
// energy added value from Tesla (which accumulates over the session).
func (s *batteryService) updateOngoingSession(ctx context.Context, log *model.ChargingLog, snap *model.BatterySnapshot) error {
	// Track the peak charge rate seen during this session.
	log.MaxChargeRate = math.Max(log.MaxChargeRate, snap.ChargeRate)
	// Tesla's charge_energy_added is cumulative for the session, so we just
	// take the latest value.
	log.EnergyAdded = snap.ChargeEnergyAdded
	log.UpdatedAt = time.Now().UTC()

	if err := s.batteryRepo.UpdateChargingLog(ctx, log); err != nil {
		return fmt.Errorf("updating ongoing charging session: %w", err)
	}
	return nil
}

// closeChargingSession fills in the end details of an open ChargingLog and
// marks it complete by setting EndedAt to the current snapshot's timestamp.
func (s *batteryService) closeChargingSession(ctx context.Context, log *model.ChargingLog, snap *model.BatterySnapshot) error {
	now := snap.SnapshotAt
	log.EndedAt = &now
	log.EndBatteryLevel = snap.BatteryLevel
	log.EnergyAdded = snap.ChargeEnergyAdded
	log.MaxChargeRate = math.Max(log.MaxChargeRate, snap.ChargeRate)
	log.UpdatedAt = time.Now().UTC()

	if err := s.batteryRepo.UpdateChargingLog(ctx, log); err != nil {
		return fmt.Errorf("closing charging session: %w", err)
	}
	return nil
}
