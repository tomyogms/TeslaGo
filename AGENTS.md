# AGENTS.md

This document provides instructions and guidelines for AI agents (and human developers) working on the TeslaGo repository.

## 1. Project Context

**TeslaGo** is a Go-based project following **Clean Architecture** and **12-Factor App** principles.

- **Language:** Go (Golang)
- **Module Name:** `github.com/tomyogms/TeslaGo`
- **Framework:** Gin
- **Database:** PostgreSQL (via GORM)
- **Testing:** Ginkgo + Gomega
- **Containerization:** Docker + Docker Compose

## 2. Project Structure

```
TeslaGo/
├── cmd/
│   └── api/
│       └── main.go                 # Application entry point
├── internal/
│   ├── config/                     # Configuration management (Env vars)
│   ├── model/                      # Data models (Entities)
│   ├── repository/                 # Database operations (Interface & Implementation)
│   ├── service/                    # Business logic (Interface & Implementation)
│   ├── handler/                    # HTTP handlers
│   ├── router/                     # Gin router setup
│   └── database/                   # DB Connection setup
├── migrations/                     # Database migrations
├── docker-compose.yaml             # Container orchestration
├── Dockerfile                      # Go application container
├── Makefile                        # Build/lint/test commands
└── go.mod                          # Dependencies
```

## 3. Build, Test, and Lint Commands

Agents should verify all changes using these commands.

### Build & Run (Docker - Recommended)
To run the full stack (API + DB):
```bash
docker-compose up --build
```

### Build (Local)
To build the application locally:
```bash
go build -o bin/api ./cmd/api
```

### Test
**Running all tests:**
```bash
go test ./...
# Or with Ginkgo
ginkgo -r
```

**Running specific tests:**
```bash
go test -v -run TestName ./internal/package
```

### Lint
We adhere to strict linting standards.
```bash
go vet ./...
# golangci-lint run (if installed)
```

## 4. Code Style & Conventions

### Architecture Layers
Follow the strict dependency rule: **Handler -> Service -> Repository -> Model**.
- **Handler:** Parses request, calls service, formats response. No business logic.
- **Service:** Contains business logic. Defines interface for repository.
- **Repository:** Handles data persistence. No business logic.
- **Model:** Pure data structures.

### Configuration
- All configuration must come from **Environment Variables**.
- Do not use config files (YAML/JSON) for application settings.
- Use `internal/config` to load variables.

### Error Handling
- Use `fmt.Errorf("...: %w", err)` to wrap errors.
- Handlers should map service errors to appropriate HTTP status codes.
- Health check returns 503 if DB is down.

### Naming
- Interfaces: `Service`, `Repository`.
- Implementations: `service`, `repository` (private structs returned by `New...` factories).

## 5. Testing Guidelines
- Use **Ginkgo** for BDD-style testing.
- Use **Gomega** for assertions.
- Mock dependencies (Repository in Service tests, Service in Handler tests).
- Run tests with `go test -v ./...` or `ginkgo -r`.

## 6. Docker
- Use `docker-compose up` to start the environment.
- The API is available at `http://localhost:8080`.
- Health check: `http://localhost:8080/health`.
