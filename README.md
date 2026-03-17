# TeslaGo

A Clean Architecture Go API for learning purposes.

## Tech Stack
- **Go 1.22+**
- **Gin** (Web Framework)
- **GORM** (ORM)
- **PostgreSQL 17** (Database)
- **Ginkgo + Gomega** (Testing)
- **Docker + Docker Compose**

## Getting Started

### Prerequisites
- Docker & Docker Compose
- Go 1.22+ (optional, for local dev)

### Run with Docker (Recommended)
This will start PostgreSQL and the Go API.
```bash
docker-compose up --build
```
The API will be available at `http://localhost:8080`.

### Health Check
```bash
curl http://localhost:8080/health
```
Response:
```json
{
  "timestamp": "2026-03-16T22:30:00Z",
  "status": "healthy",
  "database": {
    "status": "up"
  }
}
```

### Run Tests
```bash
# Run all tests
go test ./...

# Or using ginkgo
ginkgo -r
```

## Architecture
This project follows Clean Architecture principles:
- `cmd/api`: Application entry point.
- `internal/handler`: HTTP handlers (Gin).
- `internal/service`: Business logic.
- `internal/repository`: Database access (GORM).
- `internal/model`: Data entities.
