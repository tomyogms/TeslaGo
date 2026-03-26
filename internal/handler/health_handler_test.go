package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gorilla/mux"
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
		router      *mux.Router
		w           *httptest.ResponseRecorder
		req         *http.Request
	)

	BeforeEach(func() {
		mockService = &MockHealthService{}
		h = handler.NewHealthHandler(mockService)
		router = mux.NewRouter()
		router.HandleFunc("/health", h.HealthCheck).Methods(http.MethodGet)
		w = httptest.NewRecorder()
	})

	Describe("GET /health", func() {
		Context("when the service is healthy", func() {
			It("should return 200 OK", func() {
				req, _ = http.NewRequest("GET", "/health", nil)
				router.ServeHTTP(w, req)

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
				router.ServeHTTP(w, req)

				Expect(w.Code).To(Equal(http.StatusServiceUnavailable))
				Expect(w.Body.String()).To(ContainSubstring("unhealthy"))
			})
		})
	})
})
