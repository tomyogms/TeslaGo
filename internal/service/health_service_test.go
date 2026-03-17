package service_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/tomyogms/TeslaGo/internal/service"
)

// Mock Repository
type MockHealthRepository struct {
	ShouldFail bool
}

func (m *MockHealthRepository) Ping(ctx context.Context) error {
	if m.ShouldFail {
		return errors.New("db down")
	}
	return nil
}

var _ = Describe("HealthService", func() {
	var (
		svc      service.HealthService
		mockRepo *MockHealthRepository
		ctx      context.Context
	)

	BeforeEach(func() {
		mockRepo = &MockHealthRepository{}
		svc = service.NewHealthService(mockRepo)
		ctx = context.Background()
	})

	Describe("CheckHealth", func() {
		Context("when the database is up", func() {
			It("should return status healthy", func() {
				resp, err := svc.CheckHealth(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal("healthy"))
				Expect(resp.Database.Status).To(Equal("up"))
				Expect(resp.Timestamp).To(BeTemporally("~", time.Now().UTC(), time.Second))
			})
		})

		Context("when the database is down", func() {
			BeforeEach(func() {
				mockRepo.ShouldFail = true
			})

			It("should return status unhealthy", func() {
				resp, err := svc.CheckHealth(ctx)
				Expect(err).To(HaveOccurred())
				Expect(resp.Status).To(Equal("unhealthy"))
				Expect(resp.Database.Status).To(Equal("down"))
			})
		})
	})
})
