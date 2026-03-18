package service_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	extTesla "github.com/tomyogms/TeslaGo/external/tesla"
	"github.com/tomyogms/TeslaGo/internal/model"
	"github.com/tomyogms/TeslaGo/internal/service"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mocks
// ─────────────────────────────────────────────────────────────────────────────

// mockBatteryRepo implements repository.BatteryRepository in memory.
// Each field records whether the method was called and what to return.
type mockBatteryRepo struct {
	savedSnapshot   *model.BatterySnapshot
	saveSnapErr     error
	latestSnap      *model.BatterySnapshot
	latestSnapErr   error
	snapshots       []model.BatterySnapshot
	snapshotsErr    error
	deleteSnapErr   error
	savedLog        *model.ChargingLog
	saveLogErr      error
	updatedLog      *model.ChargingLog
	updateLogErr    error
	chargingLogs    []model.ChargingLog
	chargingLogsErr error
	openLog         *model.ChargingLog
	openLogErr      error
	deleteLogsErr   error
}

func (m *mockBatteryRepo) SaveSnapshot(_ context.Context, snap *model.BatterySnapshot) error {
	if m.saveSnapErr != nil {
		return m.saveSnapErr
	}
	// Assign an ID so callers can inspect what was saved.
	snap.ID = 1
	m.savedSnapshot = snap
	return nil
}

func (m *mockBatteryRepo) GetLatestSnapshot(_ context.Context, _ uint) (*model.BatterySnapshot, error) {
	return m.latestSnap, m.latestSnapErr
}

func (m *mockBatteryRepo) GetSnapshotsByVehicleAndTimeRange(_ context.Context, _ uint, _, _ time.Time) ([]model.BatterySnapshot, error) {
	return m.snapshots, m.snapshotsErr
}

func (m *mockBatteryRepo) DeleteSnapshotsOlderThan(_ context.Context, _ time.Time) error {
	return m.deleteSnapErr
}

func (m *mockBatteryRepo) SaveChargingLog(_ context.Context, log *model.ChargingLog) error {
	if m.saveLogErr != nil {
		return m.saveLogErr
	}
	log.ID = 10
	m.savedLog = log
	return nil
}

func (m *mockBatteryRepo) UpdateChargingLog(_ context.Context, log *model.ChargingLog) error {
	if m.updateLogErr != nil {
		return m.updateLogErr
	}
	m.updatedLog = log
	return nil
}

func (m *mockBatteryRepo) GetChargingLogsByVehicleAndTimeRange(_ context.Context, _ uint, _, _ time.Time, _ int) ([]model.ChargingLog, error) {
	return m.chargingLogs, m.chargingLogsErr
}

func (m *mockBatteryRepo) GetOpenChargingLog(_ context.Context, _ uint) (*model.ChargingLog, error) {
	return m.openLog, m.openLogErr
}

func (m *mockBatteryRepo) DeleteChargingLogsOlderThan(_ context.Context, _ time.Time) error {
	return m.deleteLogsErr
}

// mockVehicleDataClient implements service.TeslaVehicleDataClient.
type mockVehicleDataClient struct {
	vehicleData *extTesla.VehicleData
	err         error
}

func (m *mockVehicleDataClient) GetVehicleData(_ string, _ int64) (*extTesla.VehicleData, error) {
	return m.vehicleData, m.err
}

// mockAuthServiceForBattery satisfies the service.TeslaAuthService interface
// with minimal stubs — only GetValidAccessToken is exercised by BatteryService.
type mockAuthServiceForBattery struct {
	token    string
	tokenErr error
}

func (m *mockAuthServiceForBattery) BuildAuthURL(_, _ string) string { return "" }
func (m *mockAuthServiceForBattery) HandleCallback(_ context.Context, _, _, _ string) (*model.TeslaUser, error) {
	return nil, nil
}
func (m *mockAuthServiceForBattery) GetVehicles(_ context.Context, _ string) ([]model.TeslaVehicle, error) {
	return nil, nil
}
func (m *mockAuthServiceForBattery) GetValidAccessToken(_ context.Context, _ string) (string, error) {
	return m.token, m.tokenErr
}

// mockTeslaRepoForBattery satisfies repository.TeslaRepository with the
// minimum needed for BatteryService (user + vehicle lookups).
type mockTeslaRepoForBattery struct {
	teslaUser      *model.TeslaUser
	getUserErr     error
	vehicles       []model.TeslaVehicle
	getVehiclesErr error
}

func (m *mockTeslaRepoForBattery) UpsertTeslaUser(_ context.Context, _ *model.TeslaUser) error {
	return nil
}
func (m *mockTeslaRepoForBattery) GetTeslaUserByAdminID(_ context.Context, _ string) (*model.TeslaUser, error) {
	return m.teslaUser, m.getUserErr
}
func (m *mockTeslaRepoForBattery) UpsertTeslaVehicle(_ context.Context, _ *model.TeslaVehicle) error {
	return nil
}
func (m *mockTeslaRepoForBattery) GetVehiclesByTeslaUserID(_ context.Context, _ uint) ([]model.TeslaVehicle, error) {
	return m.vehicles, m.getVehiclesErr
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildSvc creates a BatteryService wired with the supplied mocks.
func buildSvc(battRepo *mockBatteryRepo, teslaRepo *mockTeslaRepoForBattery, auth *mockAuthServiceForBattery, client *mockVehicleDataClient) service.BatteryService {
	return service.NewBatteryService(battRepo, teslaRepo, auth, client)
}

// defaultTeslaRepo returns a repo mock pre-loaded with one user and one vehicle.
func defaultTeslaRepo() *mockTeslaRepoForBattery {
	return &mockTeslaRepoForBattery{
		teslaUser: &model.TeslaUser{ID: 1, AdminID: "admin-1"},
		vehicles: []model.TeslaVehicle{
			{ID: 5, TeslaUserID: 1, VehicleID: 99999},
		},
	}
}

// defaultVehicleData returns a Tesla API response for a disconnected vehicle.
func defaultVehicleData(chargingState string, level int, rate float64) *extTesla.VehicleData {
	return &extTesla.VehicleData{
		ID: 99999,
		ChargeState: extTesla.ChargeState{
			BatteryLevel:      level,
			BatteryRange:      200,
			ChargingState:     chargingState,
			ChargeRate:        rate,
			ChargeLimitSOC:    80,
			ChargeEnergyAdded: 5.5,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Specs
// ─────────────────────────────────────────────────────────────────────────────

var _ = Describe("BatteryService", func() {
	var (
		ctx        context.Context
		battRepo   *mockBatteryRepo
		teslaRepo  *mockTeslaRepoForBattery
		authSvc    *mockAuthServiceForBattery
		dataClient *mockVehicleDataClient
		svc        service.BatteryService
	)

	BeforeEach(func() {
		ctx = context.Background()
		battRepo = &mockBatteryRepo{}
		teslaRepo = defaultTeslaRepo()
		authSvc = &mockAuthServiceForBattery{token: "valid-token"}
		dataClient = &mockVehicleDataClient{}
		svc = buildSvc(battRepo, teslaRepo, authSvc, dataClient)
	})

	// ── GetCurrentBattery ────────────────────────────────────────────────────

	Describe("GetCurrentBattery", func() {
		Context("when the vehicle is disconnected (not charging)", func() {
			BeforeEach(func() {
				dataClient.vehicleData = defaultVehicleData("Disconnected", 75, 0)
				battRepo.openLog = nil // no open session
			})

			It("saves a snapshot and returns it", func() {
				snap, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).NotTo(HaveOccurred())
				Expect(snap).NotTo(BeNil())
				Expect(snap.BatteryLevel).To(Equal(75))
				Expect(snap.ChargingState).To(Equal("Disconnected"))
				// The snapshot should have been saved
				Expect(battRepo.savedSnapshot).NotTo(BeNil())
			})

			It("does NOT create a charging log", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).NotTo(HaveOccurred())
				Expect(battRepo.savedLog).To(BeNil())
			})
		})

		Context("when the vehicle starts charging (no open session)", func() {
			BeforeEach(func() {
				dataClient.vehicleData = defaultVehicleData("Charging", 50, 25.5)
				battRepo.openLog = nil // no prior session
			})

			It("saves a snapshot and opens a new charging session", func() {
				snap, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).NotTo(HaveOccurred())
				Expect(snap.ChargingState).To(Equal("Charging"))
				// A new ChargingLog should have been created
				Expect(battRepo.savedLog).NotTo(BeNil())
				Expect(battRepo.savedLog.StartBatteryLevel).To(Equal(50))
				Expect(battRepo.savedLog.MaxChargeRate).To(BeNumerically("==", 25.5))
			})
		})

		Context("when the vehicle is still charging (open session exists)", func() {
			BeforeEach(func() {
				dataClient.vehicleData = defaultVehicleData("Charging", 60, 30.0)
				battRepo.openLog = &model.ChargingLog{
					ID:            10,
					VehicleID:     5,
					StartedAt:     time.Now().UTC().Add(-30 * time.Minute),
					MaxChargeRate: 28.0, // will be updated to 30.0
				}
			})

			It("updates the open session with new metrics", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).NotTo(HaveOccurred())
				// Should UPDATE, not create a new log
				Expect(battRepo.savedLog).To(BeNil(), "no new log should be created")
				Expect(battRepo.updatedLog).NotTo(BeNil())
				Expect(battRepo.updatedLog.MaxChargeRate).To(BeNumerically("==", 30.0))
			})
		})

		Context("when charging completes (open session, new state is Complete)", func() {
			var sessionEndedAt time.Time

			BeforeEach(func() {
				dataClient.vehicleData = defaultVehicleData("Complete", 80, 0)
				battRepo.openLog = &model.ChargingLog{
					ID:                10,
					VehicleID:         5,
					StartedAt:         time.Now().UTC().Add(-1 * time.Hour),
					StartBatteryLevel: 40,
					MaxChargeRate:     28.0,
				}
				sessionEndedAt = time.Now().UTC()
				_ = sessionEndedAt
			})

			It("closes the open session with end details", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).NotTo(HaveOccurred())
				Expect(battRepo.updatedLog).NotTo(BeNil())
				Expect(battRepo.updatedLog.EndedAt).NotTo(BeNil())
				Expect(battRepo.updatedLog.EndBatteryLevel).To(Equal(80))
			})
		})

		Context("when the Tesla API returns an error (car asleep)", func() {
			BeforeEach(func() {
				dataClient.err = errors.New("vehicle is asleep or unreachable (408)")
			})

			It("returns an error without saving a snapshot", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("fetching vehicle data"))
				Expect(battRepo.savedSnapshot).To(BeNil())
			})
		})

		Context("when the admin is not found", func() {
			BeforeEach(func() {
				teslaRepo.getUserErr = errors.New("record not found")
			})

			It("returns an error", func() {
				_, err := svc.GetCurrentBattery(ctx, "unknown-admin", 5)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("looking up tesla user"))
			})
		})

		Context("when the vehicle ID is not found for the admin", func() {
			It("returns an error when vehicleID does not match any vehicle", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 999)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("when the access token cannot be obtained", func() {
			BeforeEach(func() {
				authSvc.tokenErr = errors.New("refresh token expired")
			})

			It("returns an error", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("getting access token"))
			})
		})

		Context("when saving the snapshot fails", func() {
			BeforeEach(func() {
				dataClient.vehicleData = defaultVehicleData("Disconnected", 50, 0)
				battRepo.saveSnapErr = errors.New("db write error")
			})

			It("returns an error", func() {
				_, err := svc.GetCurrentBattery(ctx, "admin-1", 5)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("saving battery snapshot"))
			})
		})
	})

	// ── GetBatteryHistory ────────────────────────────────────────────────────

	Describe("GetBatteryHistory", func() {
		var from, to time.Time

		BeforeEach(func() {
			to = time.Now().UTC()
			from = to.AddDate(0, 0, -7)
		})

		Context("when snapshots exist in the time range", func() {
			BeforeEach(func() {
				battRepo.snapshots = []model.BatterySnapshot{
					{ID: 1, VehicleID: 5, BatteryLevel: 80, SnapshotAt: from.Add(1 * time.Hour)},
					{ID: 2, VehicleID: 5, BatteryLevel: 75, SnapshotAt: from.Add(2 * time.Hour)},
				}
			})

			It("returns all snapshots in order", func() {
				snaps, err := svc.GetBatteryHistory(ctx, 5, from, to)
				Expect(err).NotTo(HaveOccurred())
				Expect(snaps).To(HaveLen(2))
				Expect(snaps[0].BatteryLevel).To(Equal(80))
			})
		})

		Context("when no snapshots exist", func() {
			It("returns an empty slice without error", func() {
				snaps, err := svc.GetBatteryHistory(ctx, 5, from, to)
				Expect(err).NotTo(HaveOccurred())
				Expect(snaps).To(BeEmpty())
			})
		})

		Context("when the repository returns an error", func() {
			BeforeEach(func() {
				battRepo.snapshotsErr = errors.New("db read error")
			})

			It("returns an error", func() {
				_, err := svc.GetBatteryHistory(ctx, 5, from, to)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("getting battery history"))
			})
		})
	})

	// ── GetChargingLogs ──────────────────────────────────────────────────────

	Describe("GetChargingLogs", func() {
		var from, to time.Time

		BeforeEach(func() {
			to = time.Now().UTC()
			from = to.AddDate(0, -1, 0)
		})

		Context("when charging sessions exist", func() {
			BeforeEach(func() {
				endTime := from.Add(2 * time.Hour)
				battRepo.chargingLogs = []model.ChargingLog{
					{ID: 1, VehicleID: 5, StartedAt: from, EndedAt: &endTime, EnergyAdded: 20.5},
				}
			})

			It("returns the logs", func() {
				logs, err := svc.GetChargingLogs(ctx, 5, from, to, 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(logs).To(HaveLen(1))
				Expect(logs[0].EnergyAdded).To(BeNumerically("==", 20.5))
			})
		})

		Context("when limit is 0 (use default)", func() {
			It("does not panic and returns an empty slice", func() {
				logs, err := svc.GetChargingLogs(ctx, 5, from, to, 0)
				Expect(err).NotTo(HaveOccurred())
				Expect(logs).To(BeEmpty())
			})
		})
	})

	// ── PruneOldData ─────────────────────────────────────────────────────────

	Describe("PruneOldData", func() {
		Context("when both deletions succeed", func() {
			It("returns nil", func() {
				Expect(svc.PruneOldData(ctx)).To(Succeed())
			})
		})

		Context("when snapshot deletion fails", func() {
			BeforeEach(func() {
				battRepo.deleteSnapErr = errors.New("snapshot delete error")
			})

			It("returns an error mentioning battery snapshots", func() {
				err := svc.PruneOldData(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("battery snapshots"))
			})
		})

		Context("when charging log deletion fails", func() {
			BeforeEach(func() {
				battRepo.deleteLogsErr = errors.New("log delete error")
			})

			It("returns an error mentioning charging logs", func() {
				err := svc.PruneOldData(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("charging logs"))
			})
		})
	})
})
