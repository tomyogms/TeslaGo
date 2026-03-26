# TeslaGo

A Go API that connects to the Tesla Owner API to retrieve battery status and charging logs for Tesla vehicles. Built to demonstrate **Clean Architecture** and **12-Factor App** principles in Go.

**Stack:** Go · Gorilla Mux · GORM · PostgreSQL · Ginkgo/Gomega · Docker

---

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Environment Variables](#environment-variables)
3. [Run with Docker (Recommended)](#run-with-docker-recommended)
4. [Run Locally (Without Docker)](#run-locally-without-docker)
5. [Run Tests](#run-tests)
6. [API Endpoints](#api-endpoints)
7. [Tesla OAuth Flow Walkthrough](#tesla-oauth-flow-walkthrough)
8. [Project Structure](#project-structure)
9. [Architecture](#architecture)

---

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Docker | 24+ | Required for the recommended Docker path |
| Docker Compose | V2 (`docker compose`) | Bundled with Docker Desktop |
| Go | 1.22+ | Only needed for local dev path |
| Tesla Developer Account | — | Register at https://developer.tesla.com |

---

## Environment Variables

Create a `.env` file in the project root (this file is git-ignored — never commit it):

```dotenv
# ── Application ─────────────────────────────────────────────────────────────
APP_HOST=0.0.0.0
APP_PORT=8080

# ── Database ─────────────────────────────────────────────────────────────────
# Use DB_HOST=db when running inside Docker Compose (the service name is "db").
# Use DB_HOST=localhost when running the API locally (outside Docker).
DB_HOST=db
DB_PORT=5432
DB_USER=teslago
DB_PASSWORD=secret
DB_NAME=teslago

# ── Tesla OAuth ───────────────────────────────────────────────────────────────
# From your Tesla Developer Portal app registration.
TESLA_CLIENT_ID=your-tesla-client-id
TESLA_CLIENT_SECRET=your-tesla-client-secret

# Must match exactly what is registered in the Tesla Developer Portal.
TESLA_REDIRECT_URI=http://localhost:8080/tesla/auth/callback

# Tesla API base URLs (use the .cn variants for China region).
TESLA_AUTH_BASE_URL=https://auth.tesla.com
TESLA_API_BASE_URL=https://owner-api.teslamotors.com

# AES-256-GCM key used to encrypt Tesla tokens at rest in the database.
# Generate a strong key: openssl rand -hex 32
# IMPORTANT: Do not lose this key — stored tokens become permanently unreadable.
TESLA_TOKEN_SECRET=replace-with-output-of-openssl-rand-hex-32
```

> **Note:** When running with Docker Compose, `DB_HOST` must be `db` (the Compose service name). When running the API locally with a separate `docker-compose up db`, set `DB_HOST=localhost`.

---

## Run with Docker (Recommended)

This starts both PostgreSQL and the Go API in containers.

**Step 1 — Clone the repo**
```bash
git clone git@github.com:tomyogms/TeslaGo.git
cd TeslaGo
```

**Step 2 — Create your `.env` file**

Copy the block above into `.env` and fill in your Tesla credentials and token secret.

**Step 3 — Start everything**
```bash
docker-compose up --build
```

The API is available at `http://localhost:8080`.

**Step 4 — Verify the health check**
```bash
curl http://localhost:8080/health
```

Expected response:
```json
{
  "timestamp": "2026-03-18T10:00:00Z",
  "status": "healthy",
  "database": {
    "status": "up"
  }
}
```

**Stop containers**
```bash
docker-compose down
```

---

## Run Locally (Without Docker)

Use this path when you want to run the Go binary directly (faster iteration, easier debugging).

**Step 1 — Start only the database**
```bash
docker-compose up db
```

**Step 2 — Set `DB_HOST=localhost` in your `.env`**

The API process runs outside Docker, so it reaches Postgres on `localhost`, not `db`.

```dotenv
DB_HOST=localhost
```

**Step 3 — Load env vars into your shell**
```bash
export $(grep -v '^#' .env | xargs)
```

**Step 4 — Run the API**
```bash
# Option A: using Make
make run

# Option B: directly with Go
go run cmd/api/main.go
```

**Step 5 — Verify**
```bash
curl http://localhost:8080/health
```

---

## Run Tests

Tests use [Ginkgo](https://onsi.github.io/ginkgo/) (BDD) + [Gomega](https://onsi.github.io/gomega/) (assertions). No database is required — all dependencies are mocked.

```bash
# Run all tests
go test ./...

# Run all tests with verbose output
go test -v ./...

# Run with the Ginkgo CLI (shows BDD-style output)
ginkgo -r

# Run a specific package
go test -v ./internal/service/...
go test -v ./internal/handler/...
```

---

## API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check (returns DB status) |
| `GET` | `/tesla/auth/url` | Get Tesla OAuth login URL for an admin |
| `GET` | `/tesla/auth/callback` | OAuth callback — exchanges code, saves tokens, syncs vehicles |
| `GET` | `/tesla/vehicles` | List all vehicles linked to an admin |
| `GET` | `/tesla/vehicles/{vehicleID}/battery` | Live battery status from Tesla API |
| `GET` | `/tesla/vehicles/{vehicleID}/battery-history` | Time-series battery snapshots from DB |
| `GET` | `/tesla/vehicles/{vehicleID}/charging-logs` | Historical charging sessions from DB |
| `POST` | `/tesla/admin/prune` | Delete battery snapshots and charging logs older than 90 days |

### Query Parameters

**`GET /tesla/auth/url`**
| Param | Required | Example |
|---|---|---|
| `admin_id` | Yes | `admin-1` |

**`GET /tesla/auth/callback`**
| Param | Required | Example |
|---|---|---|
| `code` | Yes | (provided by Tesla redirect) |
| `state` | Yes | (provided by Tesla redirect) |

**`GET /tesla/vehicles`**
| Param | Required | Example |
|---|---|---|
| `admin_id` | Yes | `admin-1` |

**`GET /tesla/vehicles/{vehicleID}/battery`**

`{vehicleID}` is the **internal DB id** returned by `GET /tesla/vehicles` — not Tesla's external vehicle id.

| Param | Required | Example |
|---|---|---|
| `admin_id` | Yes | `admin-1` |

**`GET /tesla/vehicles/{vehicleID}/battery-history`**
| Param | Required | Example |
|---|---|---|
| `start_date` | Yes | `2025-01-01T00:00:00Z` |
| `end_date` | Yes | `2025-12-31T23:59:59Z` |

**`GET /tesla/vehicles/{vehicleID}/charging-logs`**
| Param | Required | Example |
|---|---|---|
| `start_date` | Yes | `2025-01-01T00:00:00Z` |
| `end_date` | Yes | `2025-12-31T23:59:59Z` |
| `limit` | No | `100` (default: 50) |

---

## Tesla OAuth Flow Walkthrough

TeslaGo uses the **PKCE OAuth 2.0** flow — no client secret is transmitted during the browser redirect, making it safer for server-side apps.

### Step 1 — Get the login URL

```bash
curl "http://localhost:8080/tesla/auth/url?admin_id=admin-1"
```

Response:
```json
{
  "url": "https://auth.tesla.com/oauth2/v3/authorize?..."
}
```

### Step 2 — Open the URL in a browser

The admin visits the URL, logs into their Tesla account, and approves access. Tesla redirects back to `TESLA_REDIRECT_URI` with `?code=...&state=...` appended.

### Step 3 — Callback is handled automatically

TeslaGo's callback handler (`GET /tesla/auth/callback`) receives the redirect, exchanges the code for access + refresh tokens, encrypts and saves them to the database, and syncs the admin's vehicles.

### Step 4 — List linked vehicles

```bash
curl "http://localhost:8080/tesla/vehicles?admin_id=admin-1"
```

Note the `id` field in the response — this is your `vehicleID` for all subsequent requests.

### Step 5 — Get live battery status

```bash
curl "http://localhost:8080/tesla/vehicles/1/battery?admin_id=admin-1"
```

> **Note:** If the car is asleep, Tesla returns HTTP 408 and TeslaGo maps this to `503 Service Unavailable` with a clear message. Wake the car from the Tesla mobile app first.

---

## Project Structure

```
TeslaGo/
├── cmd/
│   └── api/
│       └── main.go                    # Entry point — wires config, DB, router
├── external/
│   └── tesla/
│       ├── client.go                  # Tesla HTTP client (token exchange, vehicle data)
│       ├── crypto.go                  # AES-256-GCM token encryption/decryption
│       └── pkce.go                    # OAuth PKCE code verifier/challenge generation
├── internal/
│   ├── config/
│   │   └── config.go                  # All env var loading (12-Factor)
│   ├── database/
│   │   └── database.go                # PostgreSQL connection + AutoMigrate
│   ├── handler/                       # HTTP layer — parse request, call service, format response
│   │   ├── health_handler.go
│   │   ├── tesla_auth_handler.go
│   │   └── battery_handler.go
│   ├── model/                         # GORM data entities (no business logic)
│   │   ├── tesla_user.go
│   │   ├── tesla_vehicle.go
│   │   ├── battery_snapshot.go
│   │   └── charging_log.go
│   ├── repository/                    # Database access (interface + GORM implementation)
│   │   ├── health_repository.go
│   │   ├── tesla_repository.go
│   │   └── battery_repository.go
│   ├── router/
│   │   └── router.go                  # Gorilla Mux router — composition root, all DI wiring
│   └── service/                       # Business logic (interface + implementation)
│       ├── health_service.go
│       ├── tesla_auth_service.go
│       └── battery_service.go
├── migrations/                        # Raw SQL migration files
│   ├── 001_create_tesla_users.sql
│   ├── 002_create_tesla_vehicles.sql
│   ├── 003_create_battery_snapshots.sql
│   └── 004_create_charging_logs.sql
├── RELEASE.md                         # Per-phase release notes
├── Makefile                           # build, run, test, docker-up, docker-down
├── Dockerfile
├── docker-compose.yaml
└── go.mod
```

---

## Architecture

TeslaGo follows **Clean Architecture**. Dependencies only point inward — outer layers depend on inner layers, never the reverse.

```
HTTP Request
     │
     ▼
┌─────────────┐
│   Handler   │  ← Parses request, calls service, formats HTTP response
└──────┬──────┘     No business logic here
       │
       ▼
┌─────────────┐
│   Service   │  ← All business logic lives here
└──────┬──────┘     Defines the Repository interface it needs
       │
       ▼
┌──────────────┐
│  Repository  │  ← Implements data persistence (GORM + PostgreSQL)
└──────┬───────┘     No business logic
       │
       ▼
┌─────────────┐
│    Model    │  ← Pure data structures (GORM entities)
└─────────────┘
```

**Key rules:**
- A `Handler` may only call a `Service` — never a `Repository` directly.
- A `Service` depends on a `Repository` **interface**, not the concrete GORM struct. This makes unit testing trivial (swap in a mock).
- All wiring (creating concrete structs, injecting dependencies) happens in `router/router.go`.
- Configuration comes exclusively from environment variables (`internal/config`).
