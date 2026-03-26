// Package router wires together all application components and registers HTTP routes.
//
// The router is the "composition root" of TeslaGo — the one place where all
// dependencies are created and connected. Think of it as the app's wiring diagram.
//
// Why centralise wiring here?
// ───────────────────────────
// In Clean Architecture, each layer only knows about the layer below it:
//
//	Handler → Service → Repository → Model
//
// But someone has to connect them together. That's the router's job.
// By doing all the "new()" calls here, we keep each individual component free
// of hard-coded dependencies — a component can be tested in isolation by
// constructing it with mock dependencies instead of real ones.
//
// Dependency Injection (DI) in this project:
// ──────────────────────────────────────────
// We do NOT use a DI framework. Go's explicit wiring is readable and debuggable:
//
//	repo    := repository.New...(db)
//	service := service.New...(repo, ...)
//	handler := handler.New...(service)
//	router.GET("/path", handler.Method)
//
// The *gorm.DB and *config.Config flow from main.go → SetupRouter → every component
// that needs them.
package router

import (
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	extTesla "github.com/tomyogms/TeslaGo/external/tesla"
	"github.com/tomyogms/TeslaGo/internal/config"
	"github.com/tomyogms/TeslaGo/internal/handler"
	"github.com/tomyogms/TeslaGo/internal/repository"
	"github.com/tomyogms/TeslaGo/internal/service"
)

// SetupRouter creates the Gorilla Mux router, wires all dependencies, and registers
// all HTTP routes. It returns the fully configured router ready to serve.
//
// Parameters:
//   - db:  the shared GORM database connection (from database.Connect)
//   - cfg: the application configuration (from config.LoadConfig)
func SetupRouter(db *gorm.DB, cfg *config.Config) *mux.Router {
	// Initialize the validator once. All handlers will share this instance.
	// Sharing the validator improves performance by avoiding repeated initialization.
	val := validator.New()

	// Create a new Gorilla Mux router
	r := mux.NewRouter()

	// ── Health Check ─────────────────────────────────────────────────────────
	// Wire: HealthRepository → HealthService → HealthHandler → GET /health
	// This endpoint pings the database and returns 200 (healthy) or 503 (unhealthy).
	// Docker and load balancers use this to decide whether the container is ready.
	healthRepo := repository.NewHealthRepository(db)
	healthService := service.NewHealthService(healthRepo)
	healthHandler := handler.NewHealthHandler(healthService)
	r.HandleFunc("/health", healthHandler.HealthCheck).Methods(http.MethodGet)

	// ── Tesla OAuth & Vehicles ───────────────────────────────────────────────
	// Wire: TeslaRepository → TeslaAuthService (+ TeslaAPIClient) → TeslaAuthHandler

	// Step 1: create the repository (database layer for Tesla data).
	teslaRepo := repository.NewTeslaRepository(db)

	// Step 2: create the external Tesla API HTTP client.
	// This is the only place that knows the Tesla API base URL.
	// In Phase 2 this same client will be extended for battery/charging calls.
	teslaAPIClient := extTesla.NewClient(cfg.TeslaAPIBaseURL)

	// Step 3: create the service, injecting both the DB repo and the HTTP client.
	// The service also receives OAuth config (clientID, redirectURI, etc.) from cfg.
	teslaAuthService := service.NewTeslaAuthService(
		teslaRepo,
		teslaAPIClient,
		cfg.TeslaClientID,
		cfg.TeslaRedirectURI,
		cfg.TeslaAuthBaseURL,
		cfg.TeslaTokenSecret,
	)

	// Step 4: create the handler, injecting the service and the shared validator.
	teslaAuthHandler := handler.NewTeslaAuthHandler(teslaAuthService, val)

	// Step 5: register routes for Tesla auth endpoints.
	// GET /tesla/auth/url?admin_id=<id>
	// Returns the Tesla OAuth login URL. Admin opens this in their browser.
	r.HandleFunc("/tesla/auth/url", teslaAuthHandler.GetAuthURL).Methods(http.MethodGet)

	// GET /tesla/auth/callback?code=<code>&state=<state>
	// Tesla redirects here after the admin approves. Completes the OAuth flow.
	r.HandleFunc("/tesla/auth/callback", teslaAuthHandler.Callback).Methods(http.MethodGet)

	// GET /tesla/vehicles?admin_id=<id>
	// Returns the list of Tesla vehicles linked to the admin from our database.
	r.HandleFunc("/tesla/vehicles", teslaAuthHandler.GetVehicles).Methods(http.MethodGet)

	// ── Phase 2: Battery & Charging ──────────────────────────────────────────
	// Wire: BatteryRepository + TeslaRepository → BatteryService → BatteryHandler
	//
	// BatteryService depends on BOTH repositories and on TeslaAuthService (for
	// token management). We reuse the already-constructed teslaRepo and
	// teslaAuthService from the block above — no duplication.

	batteryRepo := repository.NewBatteryRepository(db)

	// NewBatteryService wires all four dependencies:
	//   batteryRepo   — read/write snapshots and charging logs
	//   teslaRepo     — look up vehicles (to get Tesla's external vehicle_id)
	//   teslaAuthService — get a valid Bearer token before every API call
	//   teslaAPIClient   — the same HTTP client used by Phase 1
	batteryService := service.NewBatteryService(
		batteryRepo,
		teslaRepo,
		teslaAuthService,
		teslaAPIClient,
	)

	// Create the handler, injecting the service and the shared validator.
	batteryHandler := handler.NewBatteryHandler(batteryService, val)

	// Vehicle-scoped battery routes. {vehicleID} is our internal tesla_vehicles.id.
	// GET /tesla/vehicles/{vehicleID}/battery?admin_id=<id>
	// Live battery reading — calls Tesla API, saves snapshot, returns result.
	r.HandleFunc("/tesla/vehicles/{vehicleID}/battery", batteryHandler.GetCurrentBattery).Methods(http.MethodGet)

	// GET /tesla/vehicles/{vehicleID}/battery-history?start_date=&end_date=
	// Time-series battery snapshots from our database.
	r.HandleFunc("/tesla/vehicles/{vehicleID}/battery-history", batteryHandler.GetBatteryHistory).Methods(http.MethodGet)

	// GET /tesla/vehicles/{vehicleID}/charging-logs?start_date=&end_date=&limit=
	// Inferred charging sessions from our database.
	r.HandleFunc("/tesla/vehicles/{vehicleID}/charging-logs", batteryHandler.GetChargingLogs).Methods(http.MethodGet)

	// Admin maintenance routes.
	// POST /tesla/admin/prune
	// Deletes battery snapshots and charging logs older than 90 days.
	r.HandleFunc("/tesla/admin/prune", batteryHandler.PruneOldData).Methods(http.MethodPost)

	return r
}
