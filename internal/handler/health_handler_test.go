package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/tomyogms/TeslaGo/internal/handler"
	"github.com/tomyogms/TeslaGo/internal/model"
)

// Mock Service
type MockHealthService struct {
	ShouldFail bool
}

func (m *MockHealthService) CheckHealth(ctx context.Context) (model.HealthResponse, error) {
	if m.ShouldFail {
		return model.HealthResponse{
			Timestamp: time.Now().UTC(),
			Status:    "unhealthy",
			Database:  model.DatabaseStatus{Status: "down"},
		}, errors.New("service error")
	}

	return model.HealthResponse{
		Timestamp: time.Now().UTC(),
		Status:    "healthy",
		Database:  model.DatabaseStatus{Status: "up"},
	}, nil
}

var _ = Describe("HealthHandler", func() {
	var (
		h           *handler.HealthHandler
		mockService *MockHealthService
		engine      *gin.Engine
		w           *httptest.ResponseRecorder
		req         *http.Request
	)

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		mockService = &MockHealthService{}
		h = handler.NewHealthHandler(mockService)
		engine = gin.New()
		engine.GET("/health", h.HealthCheck)
		w = httptest.NewRecorder()
	})

	Describe("GET /health", func() {
		Context("when the service is healthy", func() {
			It("should return 200 OK", func() {
				req, _ = http.NewRequest("GET", "/health", nil)
				engine.ServeHTTP(w, req)

				Expect(w.Code).To(Equal(http.StatusOK))
				Expect(w.Body.String()).To(ContainSubstring("healthy"))
			})
		})

		Context("when the service is unhealthy", func() {
			BeforeEach(func() {
				mockService.ShouldFail = true
			})

			It("should return 503 Service Unavailable", func() {
				req, _ = http.NewRequest("GET", "/health", nil)
				engine.ServeHTTP(w, req)

				Expect(w.Code).To(Equal(http.StatusServiceUnavailable))
				Expect(w.Body.String()).To(ContainSubstring("unhealthy"))
			})
		})
	})
})
