// Package tesla provides an HTTP client for communicating with the Tesla Owner API.
//
// Tesla exposes a REST API at https://owner-api.teslamotors.com that lets
// authorised applications read vehicle state and issue commands. All requests
// must carry a short-lived Bearer access token that is obtained (and renewed)
// through Tesla's OAuth 2.0 / OpenID Connect server at https://auth.tesla.com.
//
// This package handles:
//   - Exchanging an OAuth authorisation code for access + refresh tokens (PKCE flow).
//   - Refreshing an expired access token using a long-lived refresh token.
//   - Fetching the list of vehicles registered to a Tesla account.
//
// Usage example:
//
//	client := tesla.NewClient("https://owner-api.teslamotors.com")
//	vehicles, err := client.GetVehicles(accessToken)
package tesla

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// userAgent is sent with every request so Tesla can identify our application.
// Tesla's API documentation recommends including your app name here.
const userAgent = "TeslaGo/1.0"

// Client is the main entry point for talking to the Tesla Owner API.
// It wraps a standard net/http.Client so we can set timeouts and reuse
// connections across requests efficiently.
type Client struct {
	// apiBaseURL is the root URL of the Tesla Owner API.
	// Global: https://owner-api.teslamotors.com
	// China:  https://owner-api.vn.cloud.tesla.cn
	apiBaseURL string

	// httpClient is the underlying HTTP client used for all outbound requests.
	// We configure a 30-second timeout so we never hang indefinitely waiting
	// for Tesla's servers.
	httpClient *http.Client
}

// NewClient creates a new Client that will talk to the given apiBaseURL.
// Inject this into services so they can be tested without real HTTP calls.
func NewClient(apiBaseURL string) *Client {
	return &Client{
		apiBaseURL: apiBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Token Exchange
// ─────────────────────────────────────────────────────────────────────────────

// TokenResponse represents the JSON body that Tesla's /oauth2/v3/token
// endpoint returns when issuing or refreshing tokens.
type TokenResponse struct {
	// AccessToken is a short-lived JWT (Bearer token) used to authenticate
	// every API call. It expires after ExpiresIn seconds (typically 8 hours).
	AccessToken string `json:"access_token"`

	// RefreshToken is a long-lived token that can be exchanged for a new
	// AccessToken when the current one expires. Treat it like a password —
	// never log it or expose it to clients.
	RefreshToken string `json:"refresh_token"`

	// ExpiresIn is the number of seconds until AccessToken expires.
	// e.g. 28800 = 8 hours.
	ExpiresIn int `json:"expires_in"`

	// TokenType is always "Bearer" for Tesla's API.
	TokenType string `json:"token_type"`
}

// ExchangeAuthCode exchanges a one-time OAuth authorization code for a pair
// of tokens (access + refresh) using the PKCE flow.
//
// How this fits into the OAuth flow:
//  1. We redirect the admin to Tesla's auth page (see pkce.go + service layer).
//  2. The admin logs in and grants permission.
//  3. Tesla redirects back to our callback URL with a short-lived `code` param.
//  4. This method sends that code — together with the original `codeVerifier`
//     we generated in step 1 — to Tesla's token endpoint to prove we initiated
//     the flow (PKCE prevents code-interception attacks).
//  5. Tesla responds with access + refresh tokens.
//
// Parameters:
//   - authBaseURL:   Tesla auth server, e.g. "https://auth.tesla.com"
//   - clientID:      Your Tesla developer app client ID ("ownerapi" for unofficial apps)
//   - code:          The authorization code from the callback query param
//   - codeVerifier:  The original random verifier string from GeneratePKCE()
//   - redirectURI:   Must match exactly what was used when building the auth URL
func (c *Client) ExchangeAuthCode(authBaseURL, clientID, code, codeVerifier, redirectURI string) (*TokenResponse, error) {
	// Build the JSON request body that Tesla expects.
	body := fmt.Sprintf(
		`{"grant_type":"authorization_code","client_id":"%s","code":"%s","code_verifier":"%s","redirect_uri":"%s"}`,
		clientID, code, codeVerifier, redirectURI,
	)

	req, err := http.NewRequest(http.MethodPost, authBaseURL+"/oauth2/v3/token", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	return c.doTokenRequest(req)
}

// RefreshToken uses a long-lived refresh token to obtain a brand new access
// token (and a new refresh token — Tesla rotates them on each refresh).
//
// Call this whenever GetValidAccessToken() detects that the stored access
// token is expired or about to expire. The service layer handles this
// automatically — callers never need to think about token expiry.
//
// Parameters:
//   - authBaseURL:    Tesla auth server base URL
//   - clientID:       Your Tesla app client ID
//   - refreshToken:   The plaintext (decrypted) refresh token from the database
func (c *Client) RefreshToken(authBaseURL, clientID, refreshToken string) (*TokenResponse, error) {
	// Tesla's refresh grant also requires the scope so it knows what
	// permissions the new token should carry.
	body := fmt.Sprintf(
		`{"grant_type":"refresh_token","client_id":"%s","refresh_token":"%s","scope":"openid email offline_access"}`,
		clientID, refreshToken,
	)

	req, err := http.NewRequest(http.MethodPost, authBaseURL+"/oauth2/v3/token", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	return c.doTokenRequest(req)
}

// doTokenRequest is a shared helper that executes a prepared HTTP request
// against Tesla's token endpoint and decodes the JSON response.
// It is unexported (lowercase) because it is an internal implementation detail.
func (c *Client) doTokenRequest(req *http.Request) (*TokenResponse, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing token request: %w", err)
	}
	// Always close the response body to release the underlying TCP connection
	// back to the connection pool for reuse.
	defer resp.Body.Close()

	// Read the full body before closing, otherwise defer will cut it short.
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response body: %w", err)
	}

	// Any non-200 status means the exchange failed (wrong code, expired, etc.).
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tesla auth returned status %d: %s", resp.StatusCode, string(rawBody))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(rawBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	return &tokenResp, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Vehicles
// ─────────────────────────────────────────────────────────────────────────────

// VehicleListResponse is the top-level JSON wrapper that Tesla wraps around
// any list response. The actual data lives in the `response` array.
type VehicleListResponse struct {
	Response []Vehicle `json:"response"`
	Count    int       `json:"count"`
}

// Vehicle represents a single Tesla vehicle as returned by the API.
// Note: Tesla uses two different ID fields:
//   - ID        → use this for Owner API calls (state, commands, etc.)
//   - VehicleID → use this for the Streaming and Autopark APIs
type Vehicle struct {
	// ID is the unique identifier for this vehicle on the Owner API.
	// Use this value when calling /api/1/vehicles/{id}/...
	ID int64 `json:"id"`

	// VehicleID is used for the Streaming API. Different from ID.
	VehicleID int64 `json:"vehicle_id"`

	// VIN is the 17-character Vehicle Identification Number stamped on the car.
	VIN string `json:"vin"`

	// DisplayName is the custom name the owner gave their car in the Tesla app.
	DisplayName string `json:"display_name"`

	// State reports whether the car is reachable right now.
	// Possible values: "online", "asleep", "offline"
	// Important: you cannot send commands to an "asleep" car without waking it first.
	State string `json:"state"`
}

// GetVehicles fetches all Tesla vehicles registered to the account associated
// with the given accessToken. This is typically called right after the OAuth
// callback to populate the database with the admin's cars.
//
// The returned slice may be empty if the account has no vehicles. An error is
// returned only when the HTTP call itself fails or Tesla returns a non-200 status.
func (c *Client) GetVehicles(accessToken string) ([]Vehicle, error) {
	req, err := http.NewRequest(http.MethodGet, c.apiBaseURL+"/api/1/vehicles", nil)
	if err != nil {
		return nil, fmt.Errorf("building vehicles request: %w", err)
	}
	// The Bearer scheme tells Tesla's API gateway to validate our JWT token.
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing vehicles request: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading vehicles response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tesla api returned status %d: %s", resp.StatusCode, string(rawBody))
	}

	var listResp VehicleListResponse
	if err := json.Unmarshal(rawBody, &listResp); err != nil {
		return nil, fmt.Errorf("decoding vehicles response: %w", err)
	}

	return listResp.Response, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Vehicle Data (battery + charging state)
// ─────────────────────────────────────────────────────────────────────────────

// VehicleDataResponse is the top-level wrapper Tesla sends back from
// GET /api/1/vehicles/{id}/vehicle_data
// The interesting bits live in the nested Response.ChargeState sub-object.
type VehicleDataResponse struct {
	Response VehicleData `json:"response"`
}

// VehicleData is the full vehicle state object. Tesla returns many sub-objects
// here (drive_state, climate_state, vehicle_state, etc.). We only decode the
// fields TeslaGo needs to avoid unnecessary coupling to the full Tesla schema.
type VehicleData struct {
	// ID is the Owner API vehicle identifier (same as Vehicle.ID).
	ID int64 `json:"id"`

	// ChargeState contains all battery and charging-related telemetry.
	// This is the only sub-object TeslaGo cares about for Phase 2.
	ChargeState ChargeState `json:"charge_state"`
}

// ChargeState mirrors the `charge_state` object inside Tesla's vehicle_data
// response. All numeric fields are zero-valued when the car is not charging.
//
// Field-by-field explanation:
//
//	BatteryLevel          — state of charge as a percentage, 0–100
//	BatteryRange          — estimated remaining range (miles)
//	ChargingState         — "Charging" | "Complete" | "Disconnected" | "Stopped"
//	ChargeRate            — current charging speed (miles of range added per hour)
//	ChargerVoltage        — actual voltage from the charger (Volts)
//	ChargerActualCurrent  — actual current from the charger (Amps)
//	ChargeLimitSOC        — driver-configured target charge percentage (0–100)
//	TimeToFullCharge      — estimated hours until charge limit is reached
//	ChargeEnergyAdded     — kWh added in the current/most-recent session
type ChargeState struct {
	BatteryLevel         int     `json:"battery_level"`
	BatteryRange         float64 `json:"battery_range"`
	ChargingState        string  `json:"charging_state"`
	ChargeRate           float64 `json:"charge_rate"`
	ChargerVoltage       int     `json:"charger_voltage"`
	ChargerActualCurrent int     `json:"charger_actual_current"`
	ChargeLimitSOC       int     `json:"charge_limit_soc"`
	TimeToFullCharge     float64 `json:"time_to_full_charge"`
	ChargeEnergyAdded    float64 `json:"charge_energy_added"`
}

// GetVehicleData fetches the current state of a single vehicle from the Tesla
// Owner API. This is the primary data source for Phase 2 battery and charging
// functionality.
//
// Tesla API endpoint:
//
//	GET /api/1/vehicles/{vehicleID}/vehicle_data
//
// Important: if the car is asleep, Tesla returns HTTP 408 (Request Timeout).
// The service layer is responsible for interpreting that and surfacing a
// helpful message to the caller. We intentionally do NOT try to wake the car
// here — waking costs $0.02 per call and should be an explicit decision.
//
// Parameters:
//   - accessToken: a valid (non-expired) Bearer token from GetValidAccessToken()
//   - vehicleID:   Tesla's Owner API vehicle ID (stored as TeslaVehicle.VehicleID)
func (c *Client) GetVehicleData(accessToken string, vehicleID int64) (*VehicleData, error) {
	// Construct the URL: /api/1/vehicles/{vehicleID}/vehicle_data
	url := fmt.Sprintf("%s/api/1/vehicles/%d/vehicle_data", c.apiBaseURL, vehicleID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building vehicle data request: %w", err)
	}
	// Attach the Bearer token — every Owner API request requires this header.
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing vehicle data request: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading vehicle data response body: %w", err)
	}

	// HTTP 408 means the vehicle is asleep and cannot be reached.
	// We return a distinct sentinel error so the service can map it to a
	// user-friendly 503 response rather than a generic 500.
	if resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("vehicle is asleep or unreachable (408): %s", string(rawBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tesla api returned status %d: %s", resp.StatusCode, string(rawBody))
	}

	var dataResp VehicleDataResponse
	if err := json.Unmarshal(rawBody, &dataResp); err != nil {
		return nil, fmt.Errorf("decoding vehicle data response: %w", err)
	}

	return &dataResp.Response, nil
}
