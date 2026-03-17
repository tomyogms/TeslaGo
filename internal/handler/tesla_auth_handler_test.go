package handler_test

import (
	"context"
	"encoding/json"
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

// ---------- Mock ----------

type mockTeslaAuthService struct {
	authURL           string
	teslaUser         *model.TeslaUser
	handleCallbackErr error
	vehicles          []model.TeslaVehicle
	getVehiclesErr    error
	accessToken       string
	getTokenErr       error
}

func (m *mockTeslaAuthService) BuildAuthURL(_, _ string) string {
	return m.authURL
}

func (m *mockTeslaAuthService) HandleCallback(_ context.Context, _, _, _ string) (*model.TeslaUser, error) {
	return m.teslaUser, m.handleCallbackErr
}

func (m *mockTeslaAuthService) GetVehicles(_ context.Context, _ string) ([]model.TeslaVehicle, error) {
	return m.vehicles, m.getVehiclesErr
}

func (m *mockTeslaAuthService) GetValidAccessToken(_ context.Context, _ string) (string, error) {
	return m.accessToken, m.getTokenErr
}

// ---------- Specs ----------

var _ = Describe("TeslaAuthHandler", func() {
	var (
		router  *gin.Engine
		mockSvc *mockTeslaAuthService
		rec     *httptest.ResponseRecorder
	)

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		mockSvc = &mockTeslaAuthService{}
		h := handler.NewTeslaAuthHandler(mockSvc)

		router = gin.New()
		router.GET("/tesla/auth/url", h.GetAuthURL)
		router.GET("/tesla/auth/callback", h.Callback)
		router.GET("/tesla/vehicles", h.GetVehicles)

		rec = httptest.NewRecorder()
	})

	Describe("GET /tesla/auth/url", func() {
		Context("when admin_id is provided", func() {
			BeforeEach(func() {
				mockSvc.authURL = "https://auth.tesla.com/oauth2/v3/authorize?client_id=ownerapi"
			})

			It("returns 200 with auth_url and state", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/auth/url?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))

				var body map[string]string
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				Expect(body["auth_url"]).To(ContainSubstring("auth.tesla.com"))
				Expect(body["state"]).NotTo(BeEmpty())
			})
		})

		Context("when admin_id is missing", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/auth/url", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})
	})

	Describe("GET /tesla/auth/callback", func() {
		Context("when code or state is missing", func() {
			It("returns 400 when code is absent", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/auth/callback?state=somestate", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})

			It("returns 400 when state is absent", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/auth/callback?code=somecode", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when state is unknown (no matching PKCE entry)", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/auth/callback?code=c&state=unknown.admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when the full flow succeeds", func() {
			It("returns 200 after a valid PKCE round-trip", func() {
				mockSvc.authURL = "https://auth.tesla.com"
				mockSvc.teslaUser = &model.TeslaUser{
					AdminID:        "admin-1",
					TokenExpiresAt: time.Now().UTC().Add(8 * time.Hour),
				}

				// Step 1: get the auth URL to register the state/verifier
				urlReq, _ := http.NewRequest(http.MethodGet, "/tesla/auth/url?admin_id=admin-1", nil)
				urlRec := httptest.NewRecorder()
				router.ServeHTTP(urlRec, urlReq)
				Expect(urlRec.Code).To(Equal(http.StatusOK))

				var urlBody map[string]string
				Expect(json.Unmarshal(urlRec.Body.Bytes(), &urlBody)).To(Succeed())
				state := urlBody["state"]

				// Step 2: simulate Tesla callback with the state we got
				callbackReq, _ := http.NewRequest(http.MethodGet, "/tesla/auth/callback?code=auth-code&state="+state, nil)
				callbackRec := httptest.NewRecorder()
				router.ServeHTTP(callbackRec, callbackReq)

				Expect(callbackRec.Code).To(Equal(http.StatusOK))
			})
		})

		Context("when the service returns an error", func() {
			It("returns 500", func() {
				mockSvc.authURL = "https://auth.tesla.com"
				mockSvc.handleCallbackErr = errors.New("tesla api failure")

				// Step 1: register state
				urlReq, _ := http.NewRequest(http.MethodGet, "/tesla/auth/url?admin_id=admin-1", nil)
				urlRec := httptest.NewRecorder()
				router.ServeHTTP(urlRec, urlReq)

				var urlBody map[string]string
				Expect(json.Unmarshal(urlRec.Body.Bytes(), &urlBody)).To(Succeed())
				state := urlBody["state"]

				// Step 2: callback triggers service error
				callbackReq, _ := http.NewRequest(http.MethodGet, "/tesla/auth/callback?code=auth-code&state="+state, nil)
				callbackRec := httptest.NewRecorder()
				router.ServeHTTP(callbackRec, callbackReq)

				Expect(callbackRec.Code).To(Equal(http.StatusInternalServerError))
			})
		})
	})

	Describe("GET /tesla/vehicles", func() {
		Context("when admin_id is provided and vehicles exist", func() {
			BeforeEach(func() {
				mockSvc.vehicles = []model.TeslaVehicle{
					{ID: 1, VehicleID: 111, DisplayName: "My Tesla", VIN: "5YJ3E1EA1LF000001"},
				}
			})

			It("returns 200 with the list of vehicles", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))

				var body map[string]interface{}
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				Expect(body["count"]).To(BeEquivalentTo(1))
			})
		})

		Context("when admin_id is missing", func() {
			It("returns 400", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when the service returns an error", func() {
			BeforeEach(func() {
				mockSvc.getVehiclesErr = errors.New("db error")
			})

			It("returns 500", func() {
				req, _ := http.NewRequest(http.MethodGet, "/tesla/vehicles?admin_id=admin-1", nil)
				router.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			})
		})
	})
})
