// Package handler contains the HTTP layer for TeslaGo.
//
// The handler's only job is to:
//  1. Parse and validate the incoming HTTP request (query params, body, headers)
//  2. Call the appropriate service method
//  3. Map the result to an HTTP response (status code + JSON body)
//
// Handlers contain NO business logic. They do not encrypt tokens, do not talk
// to databases, and do not know what a Tesla is. All of that lives in the
// service layer.
//
// tesla_auth_handler.go — handles the three Tesla OAuth endpoints.
//
// Endpoint summary:
//
//	GET /tesla/auth/url?admin_id=<id>
//	  → Returns a Tesla login URL the admin should open in their browser.
//	  → Also stores the PKCE code_verifier in memory keyed by state.
//
//	GET /tesla/auth/callback?code=<code>&state=<state>
//	  → Tesla redirects here after the admin approves.
//	  → Looks up the stored code_verifier, calls the service to complete auth.
//
//	GET /tesla/vehicles?admin_id=<id>
//	  → Returns the list of Tesla vehicles linked to the admin from our DB.
package handler

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	extTesla "github.com/tomyogms/TeslaGo/external/tesla"
	"github.com/tomyogms/TeslaGo/internal/service"
)

// Request DTOs for Tesla Auth endpoints.
//
// These structures define the HTTP request contracts for each endpoint.
// Validation tags ensure invalid requests are rejected at the HTTP boundary
// before reaching the service layer (fail fast, fail cheap principle).

// GetAuthURLRequest represents the query parameters for GET /tesla/auth/url.
type GetAuthURLRequest struct {
	AdminID string `form:"admin_id" validate:"required,max=255"`
}

// CallbackRequest represents the query parameters for GET /tesla/auth/callback.
type CallbackRequest struct {
	Code  string `form:"code" validate:"required,max=1000"`
	State string `form:"state" validate:"required,max=1000"`
}

// GetVehiclesRequest represents the query parameters for GET /tesla/vehicles.
type GetVehiclesRequest struct {
	AdminID string `form:"admin_id" validate:"required,max=255"`
}

// TeslaAuthHandler holds the service dependency, the in-memory PKCE store, and the validator.
type TeslaAuthHandler struct {
	// service is the business logic layer this handler delegates to.
	// It is injected as an interface so tests can replace it with a mock.
	service service.TeslaAuthService

	// validator is used to validate request DTOs against validation tags.
	// It is shared across all handler instances for efficiency.
	validator *validator.Validate

	// pkceStore is an in-memory map from composite state → code_verifier.
	//
	// Why in-memory?
	//   For Phase 1 (single-instance deployment) this is sufficient.
	//   In a multi-instance / horizontally-scaled deployment you would need a
	//   shared store like Redis so all instances can read the same state.
	//
	// Entry lifecycle:
	//   - Written when GetAuthURL is called (before redirect to Tesla)
	//   - Read+deleted when Callback is called (after Tesla redirects back)
	//   - Leak protection: a map entry for an abandoned flow stays forever.
	//     Phase 2 will add TTL-based expiry.
	pkceStore map[string]string

	// mu protects pkceStore from concurrent read/write races.
	// Go maps are NOT safe for concurrent access without a mutex.
	// sync.Mutex is a simple exclusive lock: Lock() blocks until the lock is free.
	mu sync.Mutex
}

// NewTeslaAuthHandler creates a new TeslaAuthHandler.
// The service and validator are injected here — the handler never creates its own dependencies.
func NewTeslaAuthHandler(svc service.TeslaAuthService, val *validator.Validate) *TeslaAuthHandler {
	return &TeslaAuthHandler{
		service:   svc,
		validator: val,
		pkceStore: make(map[string]string),
	}
}

// GetAuthURL handles GET /tesla/auth/url?admin_id=<id>
//
// This is step 1 of the OAuth flow. The admin (or a frontend on their behalf)
// calls this endpoint to get a Tesla login URL. The flow then continues in the
// admin's browser — we don't see the username/password at any point.
//
// What this handler does:
//  1. Parses and validates the request DTO (admin_id query parameter)
//  2. Generates a PKCE pair (code_verifier + code_challenge)
//  3. Generates a random state for CSRF protection
//  4. Combines state + admin_id into a "composite state" so we know who is
//     authenticating when Tesla calls back (composite = "<random>.<admin_id>")
//  5. Stores composite_state → code_verifier in memory
//  6. Asks the service to build the Tesla auth URL with the challenge + state
//  7. Returns the URL and state to the caller
//
// Response:
//
//	200 { "auth_url": "https://auth.tesla.com/...", "state": "abc123.admin-1" }
//	400 { "error": "admin_id is required" }
func (h *TeslaAuthHandler) GetAuthURL(c *gin.Context) {
	// Step 1: Parse request DTO from query parameters
	var req GetAuthURLRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	// Step 2: Validate the request DTO (fail fast at HTTP boundary)
	if err := h.validator.Struct(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin_id is required and must be at most 255 characters"})
		return
	}

	// At this point, req.AdminID is guaranteed valid (non-empty, max 255 chars)

	// Generate the PKCE pair. This creates:
	//   CodeVerifier  = 86-char random string (kept secret, stored server-side)
	//   CodeChallenge = BASE64URL(SHA256(CodeVerifier)) (sent to Tesla)
	pkce, err := extTesla.GeneratePKCE()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate PKCE challenge"})
		return
	}

	// Generate a random state to prevent CSRF attacks.
	state, err := extTesla.GenerateState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}

	// Encode admin_id into the state so the callback knows who to link.
	// Format: "<random_state>.<admin_id>"
	// The '.' separator is safe here because admin_id is validated as
	// a plain identifier (no dots). If admin IDs can contain dots, use
	// a different encoding like base64 or a separate session store.
	compositeState := state + "." + req.AdminID

	// Store the code_verifier so Callback can retrieve it.
	// We MUST lock the mutex before writing to the map.
	h.mu.Lock()
	h.pkceStore[compositeState] = pkce.CodeVerifier
	h.mu.Unlock()

	// Ask the service to build the full Tesla authorization URL.
	// The service knows the client_id, redirect_uri, and auth base URL.
	authURL := h.service.BuildAuthURL(compositeState, pkce.CodeChallenge)

	// Return both the URL (for redirecting the admin) and the state (so the
	// frontend can verify it when the callback returns).
	c.JSON(http.StatusOK, gin.H{
		"auth_url": authURL,
		"state":    compositeState,
	})
}

// Callback handles GET /tesla/auth/callback?code=<code>&state=<state>
//
// This is step 3 of the OAuth flow. Tesla redirects the admin's browser here
// after they approve access. The URL contains a one-time `code` and the same
// `state` we generated in GetAuthURL.
//
// What this handler does:
//  1. Parses and validates the request DTO (code and state query parameters)
//  2. Looks up and removes the code_verifier from the in-memory store
//     (removing prevents replay attacks — the verifier can only be used once)
//  3. Extracts admin_id from the composite state
//  4. Calls the service to complete the auth (token exchange + save + vehicle sync)
//  5. Returns success or error
//
// Response:
//
//	200 { "message": "Tesla account linked successfully", "admin_id": "...", "token_expires_at": "..." }
//	400 { "error": "code and state are required" }
func (h *TeslaAuthHandler) Callback(c *gin.Context) {
	// Step 1: Parse request DTO from query parameters
	var req CallbackRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	// Step 2: Validate the request DTO (fail fast at HTTP boundary)
	if err := h.validator.Struct(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code and state are required"})
		return
	}

	// At this point, req.Code and req.State are guaranteed valid (non-empty)

	// Look up AND delete the code_verifier atomically.
	// Deleting it means the same state cannot be used twice (replay prevention).
	h.mu.Lock()
	codeVerifier, ok := h.pkceStore[req.State]
	if ok {
		delete(h.pkceStore, req.State)
	}
	h.mu.Unlock()

	// If the state is unknown, either:
	//   a) The callback was forged (CSRF attack)
	//   b) The admin waited too long and the server restarted (in-memory loss)
	//   c) The state was already used (replay attempt)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown or expired state"})
		return
	}

	// Extract admin_id from the composite state "<random>.<admin_id>".
	adminID := extractAdminID(req.State)
	if adminID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state format"})
		return
	}

	// Delegate the real work to the service layer:
	//  - Exchange code + verifier for tokens
	//  - Encrypt and save tokens
	//  - Sync vehicles
	teslaUser, err := h.service.HandleCallback(c.Request.Context(), adminID, req.Code, codeVerifier)
	if err != nil {
		// We intentionally return a generic message here — we don't want to
		// leak internal error details (e.g. DB errors) to the caller.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete Tesla authentication"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "Tesla account linked successfully",
		"admin_id":         teslaUser.AdminID,
		"token_expires_at": teslaUser.TokenExpiresAt,
	})
}

// GetVehicles handles GET /tesla/vehicles?admin_id=<id>
//
// Returns the list of Tesla vehicles stored in our database for the given admin.
// This reads from our local database — it does NOT call the Tesla API.
//
// Query params:
//   - admin_id: the admin whose Tesla vehicles to retrieve
//
// Response:
//
//	200 { "vehicles": [...], "count": 2 }
//	400 { "error": "admin_id is required" }
func (h *TeslaAuthHandler) GetVehicles(c *gin.Context) {
	// Step 1: Parse request DTO from query parameters
	var req GetVehiclesRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	// Step 2: Validate the request DTO (fail fast at HTTP boundary)
	if err := h.validator.Struct(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin_id is required and must be at most 255 characters"})
		return
	}

	// At this point, req.AdminID is guaranteed valid (non-empty, max 255 chars)

	vehicles, err := h.service.GetVehicles(c.Request.Context(), req.AdminID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve vehicles"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"vehicles": vehicles,
		"count":    len(vehicles),
	})
}

// extractAdminID parses the admin_id from a composite state string.
//
// Format: "<random_part>.<admin_id>"
// We scan from the END of the string so that even if admin_id contains dots,
// everything after the LAST dot is treated as the admin_id.
//
// Example:
//
//	"Xk3mN9pQ.admin-123"  → "admin-123"
//	"Xk3mN9pQ."           → ""  (empty admin_id = invalid)
//	"Xk3mN9pQ"            → ""  (no dot = invalid)
func extractAdminID(compositeState string) string {
	for i := len(compositeState) - 1; i >= 0; i-- {
		if compositeState[i] == '.' {
			return compositeState[i+1:]
		}
	}
	return "" // no dot found → invalid state format
}
