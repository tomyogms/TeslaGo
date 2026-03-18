# TeslaGo — Release Notes

This document is a living record of every development phase in TeslaGo. Each phase
describes what was built, why each decision was made, how the components work together,
and how data flows through the system. It is written for someone learning Go and
software architecture, not just as a changelog.

---

## Phase 1 — Tesla OAuth Authentication & Vehicle Linking

**Goal:** Allow a TeslaGo admin to securely link their Tesla account so the application
can make API calls on their behalf. Store their vehicles in the database for future use.

---

### 1. What Was Built

| Component | File(s) | Purpose |
|---|---|---|
| Tesla API Client | `external/tesla/client.go` | HTTP calls to Tesla's Owner API (token exchange, vehicle list) |
| PKCE Helper | `external/tesla/pkce.go` | Generates OAuth security parameters (code verifier + challenge) |
| Crypto Helper | `external/tesla/crypto.go` | AES-256-GCM encrypt/decrypt for token storage |
| Models | `internal/model/tesla_user.go`, `tesla_vehicle.go` | Database entity definitions |
| Repository | `internal/repository/tesla_repository.go` | PostgreSQL read/write operations |
| Service | `internal/service/tesla_auth_service.go` | OAuth business logic, token lifecycle |
| Handler | `internal/handler/tesla_auth_handler.go` | HTTP endpoints, request/response |
| Router update | `internal/router/router.go` | Wires Tesla components, registers routes |
| Database update | `internal/database/database.go` | AutoMigrate for new models |
| Config update | `internal/config/config.go` | Tesla env vars (client ID, secret, redirect URI, etc.) |
| Migrations | `migrations/001_create_tesla_users.sql`, `002_create_tesla_vehicles.sql` | Reference SQL (AutoMigrate handles execution) |

**API Endpoints added:**

| Method | Path | Description |
|---|---|---|
| `GET` | `/tesla/auth/url?admin_id=<id>` | Returns the Tesla OAuth login URL |
| `GET` | `/tesla/auth/callback?code=<code>&state=<state>` | Handles Tesla's redirect after login |
| `GET` | `/tesla/vehicles?admin_id=<id>` | Lists linked Tesla vehicles for an admin |

---

### 2. Architecture Overview

TeslaGo follows **Clean Architecture**. Dependencies only point inward:

```
┌────────────────────────────────────────────────────────────┐
│                        HTTP Request                        │
└─────────────────────────────┬──────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────┐
│                  Handler Layer (internal/handler)          │
│  • Parses HTTP request params                              │
│  • Validates input                                         │
│  • Calls service                                           │
│  • Formats HTTP response (JSON + status code)              │
│  • Knows NOTHING about databases or Tesla API              │
└─────────────────────────────┬──────────────────────────────┘
                              │ calls
                              ▼
┌────────────────────────────────────────────────────────────┐
│                  Service Layer (internal/service)          │
│  • Contains ALL business logic                             │
│  • Orchestrates multi-step operations                      │
│  • Manages token encryption/decryption                     │
│  • Manages token expiry + refresh                          │
│  • Knows about models, but NOT about HTTP or SQL           │
└────────────────┬────────────────────────┬──────────────────┘
                 │ calls                  │ calls
                 ▼                        ▼
┌───────────────────────────┐  ┌──────────────────────────────┐
│  Repository Layer         │  │  External Tesla API Client   │
│  (internal/repository)    │  │  (external/tesla)            │
│                           │  │                              │
│  • SQL via GORM           │  │  • HTTP to auth.tesla.com    │
│  • CRUD operations        │  │  • HTTP to owner-api.tesla.. │
│  • No business logic      │  │  • PKCE helpers              │
│  • Returns models         │  │  • AES-256-GCM crypto        │
└───────────┬───────────────┘  └──────────────────────────────┘
            │ reads/writes
            ▼
┌────────────────────────────────────────────────────────────┐
│                      PostgreSQL Database                   │
│   tesla_users table   │   tesla_vehicles table             │
└────────────────────────────────────────────────────────────┘
```

**Key rule:** A layer only imports the layer directly below it. The model layer
has zero imports of other internal packages — it is the foundation everything
else builds on.

---

### 3. Component Deep-Dives

#### 3.1 Config (`internal/config/config.go`)

The config package is the first thing `main.go` calls. It reads all values from
environment variables using `os.LookupEnv`. In development, a `.env` file is loaded
by the `godotenv` library so you don't need to set vars manually in your terminal.

**How it works:**
```
main.go
  └─▶ config.LoadConfig()
        └─▶ godotenv.Load()         ← reads .env file if it exists
        └─▶ os.LookupEnv("KEY")     ← reads each variable
        └─▶ returns *Config          ← passed to every component that needs it
```

**Tesla-specific vars added in Phase 1:**
```
TESLA_CLIENT_ID       Your Tesla app's OAuth client ID
TESLA_CLIENT_SECRET   Your Tesla app's client secret
TESLA_REDIRECT_URI    Where Tesla sends users after login (must match developer portal)
TESLA_AUTH_BASE_URL   https://auth.tesla.com  (OAuth server)
TESLA_API_BASE_URL    https://owner-api.teslamotors.com  (REST API)
TESLA_TOKEN_SECRET    32+ char random string used as AES-256 encryption key
```

---

#### 3.2 Database (`internal/database/database.go`)

Opens one PostgreSQL connection pool shared by all repositories. Also runs
`AutoMigrate` to create any missing tables on startup.

**How GORM AutoMigrate works:**
```
db.AutoMigrate(&model.TeslaUser{}, &model.TeslaVehicle{})

For each struct, GORM:
  1. Checks if the table exists            → CREATE TABLE if not
  2. Compares struct fields to DB columns  → ALTER TABLE ADD COLUMN if field is new
  3. Does NOT drop columns                 → safe to run on live data
```

---

#### 3.3 Models (`internal/model/`)

Models are pure Go structs. They have no methods, no logic — just data.

```
TeslaUser
├── ID              uint       primary key, auto-increment
├── AdminID         string     unique — one Tesla account per admin
├── AccessToken     string     AES-256-GCM encrypted, never in JSON output
├── RefreshToken    string     AES-256-GCM encrypted, never in JSON output
├── TokenExpiresAt  time.Time  when to trigger a token refresh
├── CreatedAt       time.Time  set automatically by GORM
└── UpdatedAt       time.Time  set automatically by GORM

TeslaVehicle
├── ID           uint    primary key, auto-increment
├── TeslaUserID  uint    foreign key → TeslaUser.ID
├── VehicleID    int64   Tesla's Owner API vehicle identifier
├── DisplayName  string  e.g. "My Model 3"
├── VIN          string  17-char vehicle identification number
├── State        string  online / asleep / offline
├── CreatedAt    time.Time
└── UpdatedAt    time.Time
```

**Data relationship:**
```
Admin (TeslaGo user)
  └─▶ TeslaUser (1 per admin — stores encrypted Tesla OAuth tokens)
        └─▶ TeslaVehicle  (many — each car on the Tesla account)
        └─▶ TeslaVehicle
        └─▶ ...
```

---

#### 3.4 External Tesla API Client (`external/tesla/`)

This package contains three files that together handle all communication with Tesla:

**`client.go`** — HTTP client:
```
NewClient(apiBaseURL)
  ├── ExchangeAuthCode(...)   POST /oauth2/v3/token  (code → tokens)
  ├── RefreshToken(...)       POST /oauth2/v3/token  (refresh → new tokens)
  └── GetVehicles(token)      GET  /api/1/vehicles
```

**`pkce.go`** — OAuth security:
```
GeneratePKCE()
  ├── CodeVerifier  = base64url(random 64 bytes)           ← kept secret server-side
  └── CodeChallenge = base64url(SHA256(CodeVerifier))      ← sent to Tesla

GenerateState()
  └── returns base64url(random 16 bytes)                   ← CSRF protection token
```

**`crypto.go`** — Token encryption:
```
Encrypt(plaintext, key)
  1. SHA256(key)          → 32-byte AES key
  2. rand.Read(12 bytes)  → nonce (unique per call)
  3. AES-256-GCM.Seal()  → nonce || ciphertext || auth-tag
  4. base64url(result)    → safe string for DB storage

Decrypt(ciphertext, key)
  1. base64url.Decode()   → raw bytes
  2. split nonce prefix   → nonce + encrypted data
  3. AES-256-GCM.Open()  → verifies auth tag, decrypts
  4. returns plaintext string
```

---

#### 3.5 Repository (`internal/repository/tesla_repository.go`)

The repository translates between Go structs and PostgreSQL rows. It uses GORM's
`OnConflict` clause to implement "upsert" (insert-or-update) behaviour.

**Why upsert?**
- The same admin can re-link their Tesla account (e.g. after token expiry). Instead of
  checking "does this row exist? update or insert?" — which has a race condition — we use
  a single atomic SQL statement: `INSERT ... ON CONFLICT (admin_id) DO UPDATE SET ...`

```
TeslaRepository interface (what service sees):
  ├── UpsertTeslaUser(ctx, *TeslaUser) error
  ├── GetTeslaUserByAdminID(ctx, adminID) (*TeslaUser, error)
  ├── UpsertTeslaVehicle(ctx, *TeslaVehicle) error
  └── GetVehiclesByTeslaUserID(ctx, teslaUserID) ([]TeslaVehicle, error)

teslaRepository struct (private concrete implementation):
  └── db *gorm.DB   ← injected, never created internally
```

**GORM method choice:**
```
First()  → returns 1 row, errors if 0 rows found  (used for GetTeslaUser)
Find()   → returns all rows, empty slice if 0 rows (used for GetVehicles)
Create() → INSERT (with OnConflict = upsert)       (used for both upserts)
```

---

#### 3.6 Service (`internal/service/tesla_auth_service.go`)

The service is the brain of Phase 1. It orchestrates the OAuth flow and manages the
token lifecycle.

**Key design: two interfaces**

The service defines `TeslaAPIClient` as an interface. This is unusual — normally the
*provider* defines the interface. But in Go, the *consumer* defines the interface it
needs. This means:
- The service can be tested by injecting a `mockTeslaAPIClient` (no real HTTP)
- The real `*tesla.Client` from `external/tesla` automatically satisfies the interface
  because it has all the required methods

```
TeslaAuthService interface (what handler sees):
  ├── BuildAuthURL(state, challenge) string
  ├── HandleCallback(ctx, adminID, code, verifier) (*TeslaUser, error)
  ├── GetVehicles(ctx, adminID) ([]TeslaVehicle, error)
  └── GetValidAccessToken(ctx, adminID) (string, error)

TeslaAPIClient interface (what service needs from external Tesla):
  ├── ExchangeAuthCode(...)
  ├── RefreshToken(...)
  └── GetVehicles(token)
```

**Token lifecycle managed by the service:**
```
On every request needing a token:
  GetValidAccessToken(ctx, adminID)
    ├── Load TeslaUser from DB
    ├── Is TokenExpiresAt within 5 minutes?
    │     YES → RefreshToken() → Encrypt new tokens → Save to DB
    │     NO  → continue
    └── Decrypt AccessToken → return plaintext
```

---

#### 3.7 Handler (`internal/handler/tesla_auth_handler.go`)

The handler is the HTTP boundary of the application. It translates between
the HTTP world (query params, JSON bodies, status codes) and the Go world
(function calls, structs, errors).

**In-memory PKCE store:**
```
pkceStore  map[string]string   compositeState → codeVerifier
mu         sync.Mutex          protects the map from concurrent access
```

The map uses a `sync.Mutex` because Gin handles each HTTP request in its own
goroutine (a lightweight thread). Without the mutex, two simultaneous requests
could corrupt the map's internal structure.

---

### 4. The Complete OAuth Flow (Step by Step)

This is the most important thing to understand in Phase 1. Here is exactly what
happens when an admin links their Tesla account:

```
Admin Browser          TeslaGo Backend              Tesla Auth Server
     │                       │                             │
     │  GET /tesla/auth/url   │                             │
     │  ?admin_id=admin-1    │                             │
     │──────────────────────▶│                             │
     │                       │ GeneratePKCE()              │
     │                       │  verifier  = "Abc123..."    │
     │                       │  challenge = SHA256(verif.) │
     │                       │                             │
     │                       │ GenerateState()             │
     │                       │  state = "Xk3m...admin-1"   │
     │                       │                             │
     │                       │ Store: state → verifier     │
     │                       │ (in-memory pkceStore)       │
     │                       │                             │
     │  200 { auth_url, state}│                             │
     │◀──────────────────────│                             │
     │                       │                             │
     │  Redirect browser to auth_url                       │
     │─────────────────────────────────────────────────────▶
     │                       │                             │
     │                       │    Admin logs in to Tesla   │
     │                       │    Admin approves TeslaGo   │
     │                       │                             │
     │  GET /tesla/auth/callback                           │
     │  ?code=AUTH_CODE                                    │
     │  &state=Xk3m...admin-1│                             │
     │──────────────────────▶│                             │
     │                       │ Look up state in pkceStore  │
     │                       │  → found verifier "Abc123.."│
     │                       │ Delete state from store     │
     │                       │ Extract admin_id from state │
     │                       │                             │
     │                       │ POST /oauth2/v3/token       │
     │                       │  code=AUTH_CODE             │
     │                       │  code_verifier=Abc123...    │
     │                       │─────────────────────────────▶
     │                       │                             │
     │                       │  { access_token,            │
     │                       │    refresh_token,           │
     │                       │    expires_in: 28800 }      │
     │                       │◀────────────────────────────│
     │                       │                             │
     │                       │ Encrypt(access_token)       │
     │                       │ Encrypt(refresh_token)      │
     │                       │ UpsertTeslaUser → PostgreSQL│
     │                       │                             │
     │                       │ GetVehicles(access_token)   │
     │                       │─────────────────────────────▶
     │                       │  [{ id, vin, name, state }] │
     │                       │◀────────────────────────────│
     │                       │ UpsertVehicles → PostgreSQL │
     │                       │                             │
     │  200 { "Tesla account  │                             │
     │  linked successfully"} │                             │
     │◀──────────────────────│                             │
```

---

### 5. Security Design

| Threat | Mitigation |
|---|---|
| **Token theft from DB** | Tokens encrypted with AES-256-GCM before writing to DB |
| **Token exposed in logs** | `json:"-"` on token fields; generic error messages to HTTP |
| **CSRF (cross-site request forgery)** | Random `state` param; checked in callback |
| **Authorization code interception** | PKCE: code_verifier proves the requester initiated the flow |
| **Replay attack on callback** | `state` entry deleted from store on first use |
| **Expired tokens** | Automatic refresh 5 min before expiry in `GetValidAccessToken` |
| **Weak encryption key** | SHA-256 key derivation ensures 32-byte AES key regardless of input |

---

### 6. Database Schema (Phase 1)

```sql
-- tesla_users: one row per admin who has linked their Tesla account
CREATE TABLE tesla_users (
    id               SERIAL PRIMARY KEY,
    admin_id         VARCHAR(255) NOT NULL UNIQUE,   -- TeslaGo admin identifier
    access_token     TEXT NOT NULL,                  -- AES-256-GCM encrypted
    refresh_token    TEXT NOT NULL,                  -- AES-256-GCM encrypted
    token_expires_at TIMESTAMP NOT NULL,             -- when to refresh the access token
    created_at       TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMP NOT NULL DEFAULT NOW()
);

-- tesla_vehicles: one row per Tesla car, linked to a tesla_users row
CREATE TABLE tesla_vehicles (
    id             SERIAL PRIMARY KEY,
    tesla_user_id  INT NOT NULL REFERENCES tesla_users(id) ON DELETE CASCADE,
    vehicle_id     BIGINT NOT NULL,                   -- Tesla's Owner API vehicle ID
    display_name   VARCHAR(255),                      -- e.g. "My Model 3"
    vin            VARCHAR(17),                       -- Vehicle Identification Number
    state          VARCHAR(50),                       -- online / asleep / offline
    created_at     TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(tesla_user_id, vehicle_id)                 -- same car can't appear twice per user
);
```

**ON DELETE CASCADE** on `tesla_vehicles.tesla_user_id` means: if a TeslaUser row is
deleted, all their vehicles are automatically deleted too. No orphan rows.

---

### 7. Testing Strategy

All tests follow the Ginkgo BDD style:

```go
Describe("the subject under test") {
    Context("when some condition is true") {
        It("should produce this result") {
            Expect(result).To(Equal(expected))
        }
    }
}
```

**Service tests** (`tesla_auth_service_test.go`):
- Use `mockTeslaRepo` (in-memory map, no PostgreSQL required)
- Use `mockTeslaAPIClient` (returns canned responses, no HTTP required)
- Test: BuildAuthURL, HandleCallback success/failure, GetVehicles, GetValidAccessToken (valid + expired)
- **10 specs, all passing**

**Handler tests** (`tesla_auth_handler_test.go`):
- Use `mockTeslaAuthService` (no service or DB required)
- Use `httptest.NewRecorder()` to capture HTTP responses without a real server
- Test: GetAuthURL with/without admin_id, Callback with valid/unknown state, GetVehicles
- **12 specs, all passing**

**Running tests:**
```bash
go test -v ./...
# or
ginkgo -r
```

---

### 8. Environment Variables Reference

Create a `.env` file in the project root for local development:

```env
# Application
APP_HOST=0.0.0.0
APP_PORT=8080

# PostgreSQL
DB_HOST=localhost
DB_PORT=5432
DB_USER=teslago
DB_PASSWORD=secret
DB_NAME=teslago

# Tesla OAuth (required for Phase 1 features)
TESLA_CLIENT_ID=your_tesla_client_id
TESLA_CLIENT_SECRET=your_tesla_client_secret
TESLA_REDIRECT_URI=http://localhost:8080/tesla/auth/callback
TESLA_AUTH_BASE_URL=https://auth.tesla.com
TESLA_API_BASE_URL=https://owner-api.teslamotors.com
TESLA_TOKEN_SECRET=generate_with_openssl_rand_hex_32
```

**Generating a secure token secret:**
```bash
openssl rand -hex 32
# Example output: 4a7b3c9d2e1f8a0b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2
```

---

### 9. How to Run (Phase 1)

**Using Docker Compose (recommended):**
```bash
# Start PostgreSQL + API
docker-compose up --build

# Test the endpoints
curl "http://localhost:8080/health"
curl "http://localhost:8080/tesla/auth/url?admin_id=my-admin"
```

**Local Go build:**
```bash
go build -o bin/api ./cmd/api
./bin/api
```

---

### 10. Known Limitations & Phase 2 Plans

| Limitation | Resolution in Phase 2 |
|---|---|
| PKCE store is in-memory (lost on restart) | Replace with Redis or DB-backed session store |
| No authentication on endpoints (any caller can use any admin_id) | Add admin authentication middleware |
| Vehicles only synced at OAuth callback | Add explicit POST /tesla/vehicles/sync endpoint |
| No battery or charging data yet | **Phase 2: Battery Status & Charging Logs** |

---

## Phase 2 — Battery Status & Charging Logs

**Goal:** Expose current battery state and historical charging data for linked Tesla vehicles.
Because Tesla's API has no native charging history endpoint, we build our own by polling
`vehicle_data` on demand and inferring charging sessions from state transitions.

---

### 1. What Was Built

| Component | File(s) | Purpose |
|---|---|---|
| Models | `internal/model/battery_snapshot.go`, `charging_log.go` | DB entities for snapshots and sessions |
| Tesla Client extension | `external/tesla/client.go` | Added `GetVehicleData()`, `VehicleData`, `ChargeState` types |
| Migrations | `migrations/003_create_battery_snapshots.sql`, `004_create_charging_logs.sql` | Reference SQL DDL |
| Repository | `internal/repository/battery_repository.go` | All DB ops for snapshots and charging logs |
| Service | `internal/service/battery_service.go` | Business logic, charging session inference, 90-day pruning |
| Handler | `internal/handler/battery_handler.go` | 4 HTTP endpoints |
| Router update | `internal/router/router.go` | Wires new components, registers routes |
| Database update | `internal/database/database.go` | AutoMigrate for 2 new models |
| Tests | `battery_service_test.go`, `battery_handler_test.go` | 28 service + 15 handler specs |

---

### 2. New HTTP Endpoints

#### `GET /tesla/vehicles/:vehicleID/battery?admin_id=<id>`

**"Write-through read"**: every call to this endpoint both retrieves AND records the
current battery state. This is how we build battery history — we have no other
mechanism to get past data from Tesla.

**Flow:**
```
Client
  → BatteryHandler.GetCurrentBattery
      → TeslaRepository.GetTeslaUserByAdminID    (find the admin)
      → TeslaRepository.GetVehiclesByTeslaUserID  (find the vehicle + Tesla external ID)
      → TeslaAuthService.GetValidAccessToken       (auto-refreshes if near expiry)
      → TeslaClient.GetVehicleData                 (HTTP GET to Tesla Owner API)
      → BatteryRepository.SaveSnapshot             (persist the reading)
      → updateChargingSession                      (infer session state change)
  ← 200 { "snapshot": { ... } }
```

**Special case — car is asleep:**
Tesla returns HTTP 408 when the vehicle is asleep. The client wraps this with an
"asleep or unreachable (408)" error message. The handler detects this string and
returns **503 Service Unavailable** rather than 500, so callers know to retry later.

---

#### `GET /tesla/vehicles/:vehicleID/battery-history?start_date=&end_date=`

Returns stored `BatterySnapshot` rows in a time window. This is a pure database read —
no Tesla API call is made. Dates must be in RFC3339 format (`2025-01-01T00:00:00Z`).

```json
{
  "snapshots": [ { "battery_level": 80, "charging_state": "Disconnected", ... } ],
  "count": 14
}
```

---

#### `GET /tesla/vehicles/:vehicleID/charging-logs?start_date=&end_date=&limit=`

Returns inferred `ChargingLog` rows. Sessions are ordered newest-first. `limit` defaults
to 100. In-progress sessions have `ended_at: null`.

```json
{
  "charging_logs": [
    {
      "started_at": "2025-03-10T20:00:00Z",
      "ended_at": "2025-03-10T22:30:00Z",
      "start_battery_level": 30,
      "end_battery_level": 80,
      "energy_added": 22.5,
      "max_charge_rate": 31.2
    }
  ],
  "count": 1
}
```

---

#### `POST /tesla/admin/prune`

Triggers the 90-day retention job. Deletes all `battery_snapshots` and `charging_logs`
where the timestamp is older than 90 days. Both tables are pruned independently so a
failure in one does not prevent the other.

---

### 3. Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│  Phase 2 — Battery & Charging Data Flow                             │
│                                                                     │
│  Client                                                             │
│    │                                                                │
│    ▼                                                                │
│  BatteryHandler  ──────────────────────────────────────────────┐   │
│    │                                                            │   │
│    │  (validates params, maps errors to status codes)          │   │
│    ▼                                                            │   │
│  BatteryService                                                 │   │
│    ├── TeslaAuthService.GetValidAccessToken()  (token mgmt)    │   │
│    ├── TeslaRepository.GetTeslaUserByAdminID() (find admin)    │   │
│    ├── TeslaRepository.GetVehiclesByTeslaUserID() (find car)   │   │
│    ├── TeslaClient.GetVehicleData()  ──► Tesla Owner API       │   │
│    ├── BatteryRepository.SaveSnapshot()       (persist)        │   │
│    └── updateChargingSession()                                  │   │
│         ├── BatteryRepository.GetOpenChargingLog()             │   │
│         ├── BatteryRepository.SaveChargingLog()   (start)      │   │
│         └── BatteryRepository.UpdateChargingLog() (update/end) │   │
│                                                                 │   │
│  PostgreSQL                                                     │   │
│    ├── battery_snapshots  (one row per poll)                    │   │
│    └── charging_logs      (one row per session)                 │   │
└─────────────────────────────────────────────────────────────────────┘
```

---

### 4. Database Schema

#### `battery_snapshots`

| Column | Type | Notes |
|---|---|---|
| id | BIGSERIAL PK | Auto-assigned |
| vehicle_id | BIGINT FK | → tesla_vehicles.id |
| snapshot_at | TIMESTAMPTZ | When the reading was taken (indexed) |
| battery_level | INT | 0–100 % |
| battery_range | DOUBLE PRECISION | Estimated miles |
| charging_state | VARCHAR(32) | Charging / Complete / Disconnected / Stopped |
| charge_rate | DOUBLE PRECISION | Miles/hour added |
| charger_voltage | INT | Volts |
| charger_actual_current | INT | Amps |
| charge_limit_soc | INT | Driver's target % |
| time_to_full_charge | DOUBLE PRECISION | Hours |
| charge_energy_added | DOUBLE PRECISION | kWh this session |
| created_at | TIMESTAMPTZ | |

**Index:** `(vehicle_id, snapshot_at DESC)` — covers time-range queries and latest-snapshot lookups.

#### `charging_logs`

| Column | Type | Notes |
|---|---|---|
| id | BIGSERIAL PK | |
| vehicle_id | BIGINT FK | → tesla_vehicles.id |
| started_at | TIMESTAMPTZ | Session start (indexed) |
| ended_at | TIMESTAMPTZ NULL | NULL = session in progress |
| start_battery_level | INT | % at session start |
| end_battery_level | INT | % at session end (0 if in progress) |
| energy_added | DOUBLE PRECISION | kWh delivered |
| charge_limit | INT | Driver's target % |
| max_charge_rate | DOUBLE PRECISION | Peak miles/hour |
| created_at / updated_at | TIMESTAMPTZ | |

**Index:** `(vehicle_id, started_at DESC)` — covers date-range queries.

---

### 5. Charging Session Inference — State Machine

Tesla's `charging_state` field drives session detection:

```
Previous snapshot     New snapshot        Action
──────────────────    ──────────────────  ──────────────────────────────────
Disconnected/Stopped  Charging            INSERT new ChargingLog (open session)
Charging              Charging            UPDATE MaxChargeRate, EnergyAdded
Charging              Complete            UPDATE EndedAt, EndBatteryLevel (close)
Charging              Stopped             UPDATE EndedAt, EndBatteryLevel (close)
Charging              Disconnected        UPDATE EndedAt, EndBatteryLevel (close)
Complete/Disconnected Complete/Disconnected  No-op
```

"Previous state" is determined by checking `GetOpenChargingLog()` — if a session
row exists with `ended_at IS NULL`, the car was charging. If none exists, it was not.

---

### 6. 90-Day Data Retention

`BatteryService.PruneOldData()` deletes:
- `battery_snapshots` where `snapshot_at < NOW() - 90 days`
- `charging_logs` where `started_at < NOW() - 90 days`

Both deletions are attempted independently. The endpoint is `POST /tesla/admin/prune`.
In a production system this would be triggered by a scheduled cron job.

---

### 7. Testing Strategy

| Layer | Test file | Specs | Approach |
|---|---|---|---|
| Service | `battery_service_test.go` | 18 | Hand-rolled mocks for BatteryRepository, TeslaRepository, AuthService, VehicleDataClient |
| Handler | `battery_handler_test.go` | 15 | Mock BatteryService; httptest.NewRecorder |

Key scenarios covered:
- Car disconnected → snapshot saved, no charging log created
- Car starts charging → new ChargingLog opened
- Car still charging → existing log updated (max rate, energy)
- Charging completes → log closed with EndedAt and EndBatteryLevel
- Car asleep (408) → 503 response
- Invalid vehicleID, missing params → 400 responses
- PruneOldData success and failure paths

**Total tests after Phase 2: 55 passing (27 handler + 28 service)**

---

### 8. Key Design Decisions

| Decision | Rationale |
|---|---|
| Write-through read pattern | Every battery GET also writes a snapshot — no separate ingestion job needed for Phase 2 |
| Charging inference via DB state | Avoids streaming or background polling; sessions are reconstructed lazily on demand |
| `ended_at` as nullable pointer | Cleanly distinguishes "session in progress" (NULL) from "session complete" in both Go and JSON |
| Single BatteryRepository for snapshots + logs | The two models are tightly coupled; splitting them would require cross-repo dependencies |
| Best-effort session detection | Session inference errors don't invalidate the snapshot. Snapshot correctness is never compromised |
| `math.Max` for peak charge rate | Simple running maximum tracked across all Charging-state snapshots in a session |

---

### 9. Known Limitations / Future Work

| Limitation | Suggested Fix |
|---|---|
| Data only accumulates when callers hit the battery endpoint | Add a background poller (Phase 3) to capture data even when no one is querying |
| No wake-car support | Add `POST /tesla/vehicles/:id/wake` to wake sleeping vehicles before polling |
| Prune endpoint is unauthenticated | Add admin authentication middleware |
| Charging session detection relies on polling frequency | Infrequent polls may miss short sessions entirely |
| PKCE store still in-memory | Phase 3: replace with Redis or DB-backed session store |
