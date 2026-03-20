package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/tomyogms/TeslaGo/internal/handler"
	"github.com/tomyogms/TeslaGo/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock
// ─────────────────────────────────────────────────────────────────────────────

// mockBatteryService implements service.BatteryService for handler tests.
// Every field is settable per test so we can inject any response or error.
type mockBatteryService struct {
	snapshot        *model.BatterySnapshot
	getCurrentErr   error
	historySnaps    []model.BatterySnapshot
	historyErr      error
	chargingLogs    []model.ChargingLog
	chargingLogsErr error
	pruneErr        error
}

func (m *mockBatteryService) GetCurrentBattery(_ context.Context, _ string, _ uint) (*model.BatterySnapshot, error) {
	return m.snapshot, m.getCurrentErr
}

func (m *mockBatteryService) GetBatteryHistory(_ context.Context, _ uint, _, _ time.Time) ([]model.BatterySnapshot, error) {
	return m.historySnaps, m.historyErr
}

func (m *mockBatteryService) GetChargingLogs(_ context.Context, _ uint, _, _ time.Time, _ int) ([]model.ChargingLog, error) {
	return m.chargingLogs, m.chargingLogsErr
}

func (m *mockBatteryService) PruneOldData(_ context.Context) error {
	return m.pruneErr
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// validDateRange returns a pair of RFC3339 strings 7 days apart, suitable
// for start_date / end_date query params.
func validDateRange() (string, string) {
	to := time.Now().UTC().Truncate(time.Second)
	from := to.AddDate(0, 0, -7)
	return from.Format(time.RFC3339), to.Format(time.RFC3339)
}

// ─────────────────────────────────────────────────────────────────────────────
// Specs
// ─────────────────────────────────────────────────────────────────────────────

var _ = Describe("BatteryHandler", func() {
	var (
		router  *gin.Engine
		mockSvc *mockBatteryService
		rec     *httptest.ResponseRecorder
		val     *validator.Validate
	)

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		mockSvc = &mockBatteryService{}
		val = validator.New()
		h := handler.NewBatteryHandler(mockSvc, val)

		router = gin.New()
		router.GET("/tesla/vehicles/:vehicleID/battery", h.GetCurrentBattery)
		router.GET("/tesla/vehicles/:vehicleID/battery-history", h.GetBatteryHistory)
		router.GET("/tesla/vehicles/:vehicleID/charging-logs", h.GetChargingLogs)
		router.POST("/tesla/admin/prune", h.PruneOldData)

		rec = httptest.NewRecorder()
	})

	// ── GET /tesla/vehicles/:vehicleID/battery ────────────────────────────────

	Describe("GET /tesla/vehicles/:vehicleID/battery", func() {
		Context("when admin_id is missing", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when vehicleID is not a number", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/abc/battery?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when the service succeeds", func() {
			BeforeEach(func() {
				mockSvc.snapshot = &model.BatterySnapshot{
					ID:            1,
					VehicleID:     5,
					BatteryLevel:  80,
					ChargingState: "Disconnected",
					SnapshotAt:    time.Now().UTC(),
				}
			})

			It("returns 200 with the snapshot", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))

				var body map[string]interface{}
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				Expect(body["snapshot"]).NotTo(BeNil())
			})
		})

		Context("when the car is asleep", func() {
			BeforeEach(func() {
				mockSvc.getCurrentErr = errors.New("fetching vehicle data from tesla: vehicle is asleep or unreachable (408)")
			})

			It("returns 503", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
			})
		})

		Context("when the service returns a generic error", func() {
			BeforeEach(func() {
				mockSvc.getCurrentErr = errors.New("db connection lost")
			})

			It("returns 500", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			})
		})
	})

	// ── GET /tesla/vehicles/:vehicleID/battery-history ────────────────────────

	Describe("GET /tesla/vehicles/:vehicleID/battery-history", func() {
		Context("when start_date or end_date is missing", func() {
			It("returns 400 when start_date is absent", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?end_date=2025-01-31T23:59:59Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})

			It("returns 400 when end_date is absent", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=2025-01-01T00:00:00Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when dates are malformed", func() {
			It("returns 400 for invalid start_date format", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=not-a-date&end_date=2025-01-31T23:59:59Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when end_date is before start_date", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=2025-02-01T00:00:00Z&end_date=2025-01-01T00:00:00Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when the service returns snapshots", func() {
			BeforeEach(func() {
				mockSvc.historySnaps = []model.BatterySnapshot{
					{ID: 1, BatteryLevel: 80},
					{ID: 2, BatteryLevel: 75},
				}
			})

			It("returns 200 with snapshots and count", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))
				var body map[string]interface{}
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				Expect(body["count"]).To(BeEquivalentTo(2))
			})
		})

		Context("when the service returns an error", func() {
			BeforeEach(func() {
				mockSvc.historyErr = errors.New("db error")
			})

			It("returns 500", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			})
		})
	})

	// ── GET /tesla/vehicles/:vehicleID/charging-logs ──────────────────────────

	Describe("GET /tesla/vehicles/:vehicleID/charging-logs", func() {
		Context("when the service returns logs", func() {
			BeforeEach(func() {
				endTime := time.Now().UTC()
				mockSvc.chargingLogs = []model.ChargingLog{
					{ID: 1, VehicleID: 5, EnergyAdded: 20.5, EndedAt: &endTime},
				}
			})

			It("returns 200 with charging logs and count", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))
				var body map[string]interface{}
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				Expect(body["count"]).To(BeEquivalentTo(1))
			})
		})

		Context("when limit param is provided", func() {
			It("accepts a numeric limit without error", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr+"&limit=50", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})
		})
	})

	// ── POST /tesla/admin/prune ───────────────────────────────────────────────

	Describe("POST /tesla/admin/prune", func() {
		Context("when pruning succeeds", func() {
			It("returns 200", func() {
				req, _ := http.NewRequest(http.MethodPost, "/tesla/admin/prune", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))

				var body map[string]string
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				Expect(body["message"]).To(ContainSubstring("pruned"))
			})
		})

		Context("when pruning fails", func() {
			BeforeEach(func() {
				mockSvc.pruneErr = errors.New("db delete error")
			})

			It("returns 500", func() {
				req, _ := http.NewRequest(http.MethodPost, "/tesla/admin/prune", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			})
		})
	})

	// ── Validation Tests ──────────────────────────────────────────────────────

	Describe("GetCurrentBattery validation", func() {
		Context("when admin_id exceeds max length (255 chars)", func() {
			It("returns 400", func() {
				longAdminID := ""
				for i := 0; i < 256; i++ {
					longAdminID += "a"
				}
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery?admin_id="+longAdminID, nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when admin_id is empty", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery?admin_id=", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when vehicleID is zero", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/0/battery?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when admin_id is within bounds and vehicleID is valid", func() {
			BeforeEach(func() {
				mockSvc.snapshot = &model.BatterySnapshot{
					ID:            1,
					VehicleID:     5,
					BatteryLevel:  80,
					ChargingState: "Disconnected",
					SnapshotAt:    time.Now().UTC(),
				}
			})

			It("returns 200", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})
		})
	})

	Describe("GetBatteryHistory validation", func() {
		Context("when vehicleID is zero", func() {
			It("returns 400", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/0/battery-history?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when both dates are missing", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when start_date has invalid format", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=invalid-date&end_date=2025-01-31T23:59:59Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when end_date has invalid format", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=2025-01-01T00:00:00Z&end_date=invalid-date", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when end_date is before start_date", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=2025-02-01T00:00:00Z&end_date=2025-01-01T00:00:00Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when end_date equals start_date", func() {
			It("returns 400 (gtfield requires strictly greater)", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date=2025-01-01T00:00:00Z&end_date=2025-01-01T00:00:00Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when dates are valid and in correct order", func() {
			BeforeEach(func() {
				mockSvc.historySnaps = []model.BatterySnapshot{}
			})

			It("returns 200", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/battery-history?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})
		})
	})

	Describe("GetChargingLogs validation", func() {
		Context("when vehicleID is zero", func() {
			It("returns 400", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/0/charging-logs?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when both dates are missing", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when start_date has invalid format", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date=invalid-date&end_date=2025-01-31T23:59:59Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when end_date has invalid format", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date=2025-01-01T00:00:00Z&end_date=invalid-date", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when end_date is before start_date", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date=2025-02-01T00:00:00Z&end_date=2025-01-01T00:00:00Z", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when limit exceeds max (10000)", func() {
			It("returns 400", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr+"&limit=10001", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when limit is negative", func() {
			It("returns 400", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr+"&limit=-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when limit is within bounds (0-10000)", func() {
			BeforeEach(func() {
				mockSvc.chargingLogs = []model.ChargingLog{}
			})

			It("returns 200 with limit=0", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr+"&limit=0", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})

			It("returns 200 with limit=5000", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr+"&limit=5000", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})

			It("returns 200 with limit=10000 (max)", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr+"&limit=10000", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})

			It("returns 200 with no limit param (defaults to 0)", func() {
				startStr, endStr := validDateRange()
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles/5/charging-logs?start_date="+startStr+"&end_date="+endStr, nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
			})
		})
	})
})
