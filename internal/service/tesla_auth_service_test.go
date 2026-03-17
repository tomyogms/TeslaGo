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

// ---------- Mocks ----------

type mockTeslaRepo struct {
	teslaUser        *model.TeslaUser
	vehicles         []model.TeslaVehicle
	upsertUserErr    error
	upsertVehicleErr error
	getUserErr       error
	getVehiclesErr   error
}

func (m *mockTeslaRepo) UpsertTeslaUser(_ context.Context, user *model.TeslaUser) error {
	if m.upsertUserErr != nil {
		return m.upsertUserErr
	}
	m.teslaUser = user
	return nil
}

func (m *mockTeslaRepo) GetTeslaUserByAdminID(_ context.Context, _ string) (*model.TeslaUser, error) {
	if m.getUserErr != nil {
		return nil, m.getUserErr
	}
	return m.teslaUser, nil
}

func (m *mockTeslaRepo) UpsertTeslaVehicle(_ context.Context, _ *model.TeslaVehicle) error {
	return m.upsertVehicleErr
}

func (m *mockTeslaRepo) GetVehiclesByTeslaUserID(_ context.Context, _ uint) ([]model.TeslaVehicle, error) {
	if m.getVehiclesErr != nil {
		return nil, m.getVehiclesErr
	}
	return m.vehicles, nil
}

type mockTeslaAPIClient struct {
	tokenResp   *extTesla.TokenResponse
	tokenErr    error
	refreshResp *extTesla.TokenResponse
	refreshErr  error
	vehicles    []extTesla.Vehicle
	vehiclesErr error
}

func (m *mockTeslaAPIClient) ExchangeAuthCode(_, _, _, _, _ string) (*extTesla.TokenResponse, error) {
	return m.tokenResp, m.tokenErr
}

func (m *mockTeslaAPIClient) RefreshToken(_, _, _ string) (*extTesla.TokenResponse, error) {
	return m.refreshResp, m.refreshErr
}

func (m *mockTeslaAPIClient) GetVehicles(_ string) ([]extTesla.Vehicle, error) {
	return m.vehicles, m.vehiclesErr
}

// ---------- Specs ----------

var _ = Describe("TeslaAuthService", func() {
	const (
		tokenSecret = "test-secret-key"
		adminID     = "admin-123"
		clientID    = "ownerapi"
		redirectURI = "http://localhost:8080/tesla/auth/callback"
		authBaseURL = "https://auth.tesla.com"
	)

	var (
		svc        service.TeslaAuthService
		mockRepo   *mockTeslaRepo
		mockClient *mockTeslaAPIClient
		ctx        context.Context
	)

	BeforeEach(func() {
		mockRepo = &mockTeslaRepo{}
		mockClient = &mockTeslaAPIClient{}
		svc = service.NewTeslaAuthService(
			mockRepo, mockClient, clientID, redirectURI, authBaseURL, tokenSecret,
		)
		ctx = context.Background()
	})

	Describe("BuildAuthURL", func() {
		It("returns a URL containing the client_id, redirect_uri, and state", func() {
			authURL := svc.BuildAuthURL("my-state", "my-challenge")
			Expect(authURL).To(ContainSubstring("client_id=" + clientID))
			Expect(authURL).To(ContainSubstring("state=my-state"))
			Expect(authURL).To(ContainSubstring("code_challenge=my-challenge"))
			Expect(authURL).To(ContainSubstring("code_challenge_method=S256"))
		})
	})

	Describe("HandleCallback", func() {
		Context("when Tesla returns valid tokens", func() {
			BeforeEach(func() {
				mockClient.tokenResp = &extTesla.TokenResponse{
					AccessToken:  "access-token-value",
					RefreshToken: "refresh-token-value",
					ExpiresIn:    28800, // 8h
				}
				mockClient.vehicles = []extTesla.Vehicle{
					{ID: 111, VehicleID: 222, VIN: "5YJ3E1EA1LF000001", DisplayName: "My Tesla", State: "online"},
				}
			})

			It("persists the TeslaUser with encrypted tokens", func() {
				user, err := svc.HandleCallback(ctx, adminID, "auth-code", "code-verifier")
				Expect(err).NotTo(HaveOccurred())
				Expect(user.AdminID).To(Equal(adminID))
				Expect(user.AccessToken).NotTo(Equal("access-token-value"), "token should be encrypted")
				Expect(user.RefreshToken).NotTo(Equal("refresh-token-value"), "token should be encrypted")
				Expect(user.TokenExpiresAt).To(BeTemporally(">", time.Now().UTC()))
			})
		})

		Context("when the Tesla API returns an error", func() {
			BeforeEach(func() {
				mockClient.tokenErr = errors.New("tesla api unavailable")
			})

			It("returns an error", func() {
				_, err := svc.HandleCallback(ctx, adminID, "auth-code", "code-verifier")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("exchanging auth code"))
			})
		})

		Context("when the repository fails to save the user", func() {
			BeforeEach(func() {
				mockClient.tokenResp = &extTesla.TokenResponse{
					AccessToken:  "access-token-value",
					RefreshToken: "refresh-token-value",
					ExpiresIn:    28800,
				}
				mockRepo.upsertUserErr = errors.New("db error")
			})

			It("returns an error", func() {
				_, err := svc.HandleCallback(ctx, adminID, "auth-code", "code-verifier")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("saving tesla user"))
			})
		})
	})

	Describe("GetVehicles", func() {
		Context("when the admin has linked vehicles", func() {
			BeforeEach(func() {
				mockRepo.teslaUser = &model.TeslaUser{ID: 1, AdminID: adminID}
				mockRepo.vehicles = []model.TeslaVehicle{
					{ID: 1, TeslaUserID: 1, VehicleID: 111, DisplayName: "My Tesla", VIN: "5YJ3E1EA1LF000001"},
				}
			})

			It("returns the list of vehicles", func() {
				vehicles, err := svc.GetVehicles(ctx, adminID)
				Expect(err).NotTo(HaveOccurred())
				Expect(vehicles).To(HaveLen(1))
				Expect(vehicles[0].DisplayName).To(Equal("My Tesla"))
			})
		})

		Context("when the admin is not found", func() {
			BeforeEach(func() {
				mockRepo.getUserErr = errors.New("record not found")
			})

			It("returns an error", func() {
				_, err := svc.GetVehicles(ctx, adminID)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("getting tesla user"))
			})
		})
	})

	Describe("GetValidAccessToken", func() {
		Context("when the token is still valid", func() {
			BeforeEach(func() {
				encToken, _ := extTesla.Encrypt("plain-access-token", tokenSecret)
				encRefresh, _ := extTesla.Encrypt("plain-refresh-token", tokenSecret)
				mockRepo.teslaUser = &model.TeslaUser{
					ID:             1,
					AdminID:        adminID,
					AccessToken:    encToken,
					RefreshToken:   encRefresh,
					TokenExpiresAt: time.Now().UTC().Add(1 * time.Hour),
				}
			})

			It("returns the decrypted access token without calling refresh", func() {
				token, err := svc.GetValidAccessToken(ctx, adminID)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("plain-access-token"))
				// RefreshToken on the client should NOT have been called
				Expect(mockClient.refreshResp).To(BeNil())
			})
		})

		Context("when the token is expired", func() {
			BeforeEach(func() {
				encToken, _ := extTesla.Encrypt("old-access-token", tokenSecret)
				encRefresh, _ := extTesla.Encrypt("plain-refresh-token", tokenSecret)
				mockRepo.teslaUser = &model.TeslaUser{
					ID:             1,
					AdminID:        adminID,
					AccessToken:    encToken,
					RefreshToken:   encRefresh,
					TokenExpiresAt: time.Now().UTC().Add(-1 * time.Hour), // expired
				}
				mockClient.refreshResp = &extTesla.TokenResponse{
					AccessToken:  "new-access-token",
					RefreshToken: "new-refresh-token",
					ExpiresIn:    28800,
				}
			})

			It("refreshes the token and returns the new access token", func() {
				token, err := svc.GetValidAccessToken(ctx, adminID)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("new-access-token"))
			})
		})
	})
})
