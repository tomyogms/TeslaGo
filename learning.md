# Learning Guide for TeslaGo

A quick reference organized by topic. As you explore TeslaGo, questions and learnings are categorized here for easy lookup.

---

## Table of Contents

- [Database & Relationships](#database--relationships)
- [GORM & ORM](#gorm--orm)
- [Migrations](#migrations)
- [Architecture & Design Patterns](#architecture--design-patterns)
- [Testing](#testing)
- [Go Language Concepts](#go-language-concepts)
- [Configuration & Deployment](#configuration--deployment)

---

## Database & Relationships

### Q: How do GORM and PostgreSQL enforce relationships between models?

**Short Answer:**
PostgreSQL enforces relationships at the database level using `FOREIGN KEY` constraints. GORM is just the Go interface to the database — it doesn't enforce relationships in the code.

**Details:**

The relationship chain in TeslaGo:
```
Go Model (TeslaVehicle struct with TeslaUserID uint field)
    ↓
    Does NOT explicitly declare the relationship
    ↓
SQL Migration (migrations/002_create_tesla_vehicles.sql)
    ↓
    DECLARES the relationship: REFERENCES tesla_users(id) ON DELETE CASCADE
    ↓
PostgreSQL Database Engine
    ↓
    ENFORCES the relationship at query time
```

**Key File References:**
- Model: `internal/model/tesla_vehicle.go` (line 29)
- Migration: `migrations/002_create_tesla_vehicles.sql` (line 4)

**Enforcement Guarantees:**
- ✅ Cannot insert a vehicle with non-existent `tesla_user_id` → PostgreSQL rejects it
- ✅ Deleting a user cascades to all their vehicles automatically
- ✅ Database enforces this regardless of how code accesses it (GORM, raw SQL, etc.)

---

### Q: What happens to child records when a parent is deleted?

**Short Answer:**
PostgreSQL automatically deletes all child records due to `ON DELETE CASCADE` constraints.

**Example:**

```
TeslaUser (id=1)
    │
    │  ON DELETE CASCADE
    ▼
TeslaVehicle (tesla_user_id=1)
    │
    ├──── ON DELETE CASCADE ────▶ BatterySnapshot (vehicle_id=X)
    │
    └──── ON DELETE CASCADE ────▶ ChargingLog (vehicle_id=X)
```

If you `DELETE FROM tesla_users WHERE id = 1`, PostgreSQL automatically:
1. Finds all vehicles with `tesla_user_id = 1`
2. For each vehicle, deletes all battery snapshots and charging logs
3. Deletes the vehicles
4. All in one atomic transaction

**Key File References:**
- Foreign key with CASCADE: `migrations/002_create_tesla_vehicles.sql` (line 4)
- Battery snapshots cascade: `migrations/003_create_battery_snapshots.sql` (line 25)
- Charging logs cascade: `migrations/004_create_charging_logs.sql` (line 32)

---

### Q: Why doesn't the Go model explicitly declare relationships like Django?

**Short Answer:**
TeslaGo follows Clean Architecture — models stay as pure data with no infrastructure knowledge. Relationships are declared in SQL migrations, not in Go structs.

**Details:**

**Django approach (relationship in code):**
```python
# models.py
class Vehicle(models.Model):
    owner = models.ForeignKey(User, on_delete=models.CASCADE)
```

**TeslaGo approach (relationship in database):**
```go
// internal/model/tesla_vehicle.go
type TeslaVehicle struct {
    ID          uint
    TeslaUserID uint  // Just an integer field
    VehicleID   int64
}
```

**Why TeslaGo chose this:**
1. **Models are pure data** — no ORM/database knowledge
2. **Relationships enforced at DB level** — more reliable
3. **No "magic"** — cascade behavior is visible in SQL, not hidden in struct tags
4. **Better separation of concerns** — database schema vs. application logic

**What GORM COULD do (but doesn't here):**
```go
// This IS possible in GORM but NOT used in TeslaGo:
type TeslaVehicle struct {
    ID        uint
    TeslaUserID uint
    TeslaUser TeslaUser `gorm:"foreignKey:TeslaUserID;constraint:OnDelete:CASCADE"`
}
```

**Trade-offs:**
| Benefit | Trade-off |
|---------|-----------|
| ✅ Models stay pure | ❌ Relationships not visible in Go code |
| ✅ Relationships enforced at DB | ✅ No duplication or sync issues |
| ✅ Works with any code accessing the DB | ⚠️ Must check migrations to understand schema |

---

## GORM & ORM

### Q: What's the difference between GORM and Django's ORM?

**Short Answer:**
Django's ORM is tightly coupled to models and auto-generates migrations. GORM is more of a query builder with optional relationship helpers. TeslaGo uses GORM in a minimal way to keep concerns separate.

**Comparison Table:**

| Feature | Django ORM | GORM | TeslaGo Usage |
|---------|-----------|------|--------------|
| **Relationship declarations** | In model classes | Optional nested structs | Not used (in SQL instead) |
| **Migration generation** | Auto (via `makemigrations`) | Auto (via `AutoMigrate`) | Manual SQL + AutoMigrate for dev |
| **Query syntax** | `.filter()`, `.exclude()` | `.Where()`, `.Find()` | `.Where()`, `.First()` |
| **Preloading** | `.select_related()`, `.prefetch_related()` | `.Preload()` | Manual queries (in repository) |
| **Enforced schema validation** | Yes (model-driven) | No (DB-driven) | Via SQL migrations only |

---

### Q: GORM can declare relationships in models but TeslaGo doesn't. Why?

**Short Answer:**
Declaring relationships in GORM models couples the application code to the ORM. TeslaGo keeps models free of this infrastructure concern.

**The Alternative (Not Used):**
```go
type TeslaVehicle struct {
    ID          uint
    TeslaUserID uint
    // This nested struct makes the model "know" about GORM and relationships
    TeslaUser   TeslaUser `gorm:"foreignKey:TeslaUserID"`  // ← Infrastructure knowledge
}
```

**Problems with this:**
1. Model now depends on GORM tags and conventions
2. Model has "knowledge" of other models (coupling)
3. `Preload()` becomes easier but adds magic
4. Makes models harder to reuse outside GORM context

**TeslaGo's approach (Pure Data):**
```go
type TeslaVehicle struct {
    ID          uint
    TeslaUserID uint  // Just data
    VehicleID   int64
    DisplayName string
}
```

Relationships are handled in the **repository layer** (data access), not the model layer.

**Key File Reference:**
- Repository layer handles queries: `internal/repository/tesla_repository.go`
- Model is pure data: `internal/model/tesla_vehicle.go`

---

### Q: Does GORM automatically create foreign key constraints?

**Short Answer:**
No, not unless you explicitly declare the relationship in the struct with nested types and constraint tags.

**What AutoMigrate Does:**

```go
db.AutoMigrate(&model.TeslaVehicle{})
```

Creates:
```sql
CREATE TABLE tesla_vehicles (
    id              SERIAL PRIMARY KEY,
    tesla_user_id   INT NOT NULL,        -- ✅ Column exists
    vehicle_id      BIGINT NOT NULL,
    display_name    VARCHAR(255),
    -- ...
);
CREATE INDEX idx_tesla_vehicles_tesla_user_id ON tesla_vehicles(tesla_user_id);
```

Notice: **No `REFERENCES` clause!** The foreign key constraint is missing.

**What the SQL Migration Does:**

```sql
-- migrations/002_create_tesla_vehicles.sql
CREATE TABLE tesla_vehicles (
    tesla_user_id INT NOT NULL REFERENCES tesla_users(id) ON DELETE CASCADE,
    -- ✅ Constraint IS here
);
```

**Key Takeaway:**
TeslaGo relies on **SQL migrations** for foreign key constraints, not GORM's AutoMigrate. AutoMigrate only creates the basic table structure.

**Key File References:**
- AutoMigrate call: `internal/database/database.go` (lines 76–83)
- Manual constraint: `migrations/002_create_tesla_vehicles.sql` (line 4)

---

## Migrations

### Q: Are migrations auto-generated from models like Django or manually written?

**Short Answer:**
Manually written SQL. TeslaGo migrations are hand-coded `.sql` files, not generated by the framework.

**Details:**

Files like `migrations/002_create_tesla_vehicles.sql` were written by a developer. There's no `go generate` or framework tool that created them.

**TeslaGo's Dual System:**

| Phase | Tool | Method | Files |
|-------|------|--------|-------|
| **Development** | GORM AutoMigrate | Automatic (struct-driven) | None (in-memory) |
| **Production** | SQL Migrations | Manual (SQL files) | `migrations/*.sql` |

**Explanation from Code:**

In `database.go` (lines 25–29):
```
// IMPORTANT limitations of AutoMigrate:
//   - It only ADDS columns and creates tables — it never drops columns or tables.
//   - For destructive changes (renaming, dropping) you need manual SQL migrations.
//   - It is fine for development and small projects; large production systems
//     usually prefer explicit migration files (like the .sql files in /migrations).
```

**Why Manual?**

1. **Full control** — Can write any SQL (destructive changes, indexes, constraints)
2. **Auditable** — Exact SQL is in version control
3. **Safe** — Schema changes are reviewed before applying
4. **Production-ready** — No surprises from ORM magic

---

### Q: If I add a new field to a model, how do I add it to the database?

**Short Answer:**
You must manually create a SQL migration file with an `ALTER TABLE` statement.

**Step-by-Step:**

**1. Add field to Go model:**
```go
// internal/model/tesla_vehicle.go
type TeslaVehicle struct {
    ID          uint
    TeslaUserID uint
    VehicleID   int64
    DisplayName string
    Nickname    string  // ← NEW FIELD
    VIN         string
}
```

**2. Create a new migration file:**
```sql
-- migrations/005_add_nickname_to_tesla_vehicles.sql
-- +migrate Up
ALTER TABLE tesla_vehicles ADD COLUMN nickname VARCHAR(255);

-- +migrate Down
ALTER TABLE tesla_vehicles DROP COLUMN nickname;
```

**3. On startup, migrations are applied** (via AutoMigrate in dev or manual migration tool in prod)

**Note:** AutoMigrate will NOT automatically create this column from the struct. The SQL migration is what actually creates it.

**Key File References:**
- Migration examples: `migrations/` directory
- AutoMigrate: `internal/database/database.go` (lines 76–83)

---

### Q: What's the difference between `+migrate Up` and `+migrate Down`?

**Short Answer:**
`Up` applies the migration forward. `Down` rolls it back (undoes the change).

**Example:**

```sql
-- +migrate Up
-- This runs when you apply the migration
ALTER TABLE tesla_vehicles ADD COLUMN nickname VARCHAR(255);

-- +migrate Down
-- This runs if you need to rollback
ALTER TABLE tesla_vehicles DROP COLUMN nickname;
```

**Usage:**
```bash
# Apply all migrations forward
migrate -path ./migrations -database "..." up

# Rollback the last migration
migrate -path ./migrations -database "..." down

# Rollback to a specific version
migrate -path ./migrations -database "..." down 3
```

**Key Files:**
- Example with both Up/Down: `migrations/001_create_tesla_users.sql`
- All migrations follow this pattern: `migrations/00*.sql`

---

### Q: What happens if the Go model doesn't match the SQL schema?

**Short Answer:**
Nothing automatic — you'll discover the mismatch at runtime when queries fail (if you have tests) or in production (if you don't).

**Scenarios:**

**Scenario 1: Add field to model, forget SQL migration**
```go
type TeslaVehicle struct {
    Nickname string  // ← NEW
}
```
Result: GORM silently ignores the field. No error. Data doesn't save.

**Scenario 2: Add migration, forget model field**
```sql
ALTER TABLE tesla_vehicles ADD COLUMN nickname VARCHAR(255);
```
Result: Database has the column, but Go can't read it. Data exists but is inaccessible.

**Scenario 3: Schema matches ✅**
Developer manually verified or integration tests caught it.

---

### Q: How can I catch schema mismatches early?

**Short Answer:**
Integration tests against a real test database are the most practical approach.

**Approach 1: Integration Tests**
```go
// Test that all fields can be written and read
func TestSchemaMatches(t *testing.T) {
    testDB := setupTestDB()
    repo := NewTeslaRepository(testDB)
    
    vehicle := &model.TeslaVehicle{
        ID:          1,
        TeslaUserID: 1,
        Nickname:    "Tesla",  // Will error if column doesn't exist
    }
    
    err := repo.UpsertTeslaVehicle(context.Background(), vehicle)
    if err != nil {
        t.Fatalf("schema mismatch: %v", err)
    }
}
```

**Approach 2: Database Constraints**
Use `NOT NULL` to force errors:
```sql
ALTER TABLE tesla_vehicles ADD COLUMN nickname VARCHAR(255) NOT NULL DEFAULT '';
```

**Approach 3: Code Review**
Developer manually verifies models match migrations.

**Approach 4: Alternative ORMs**
- **sqlc**: Generates Go from SQL — catches mismatches at code-generation time
- **ent**: Auto-generates migrations from Go schemas — keeps them in sync

**Status in TeslaGo:**
Currently: No automatic validation. Relies on developer discipline + testing.

---

## Architecture & Design Patterns

### Q: What's the relationship flow between Handler, Service, Repository, and Model?

**Short Answer:**
One-way dependency: Handler → Service → Repository → Model. Each layer only knows about layers below it.

**Diagram:**
```
HTTP Request
     │
     ▼
┌─────────────┐
│   Handler   │  ← Parses HTTP request, calls service, formats response
└──────┬──────┘     No business logic here
       │
       ▼
┌─────────────┐
│   Service   │  ← All business logic lives here
└──────┬──────┘     Defines what Repository interface it needs
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

**Key Rules:**
1. Handler ONLY calls Service (never Repository directly)
2. Service depends on Repository **interface**, not concrete implementation
3. Repository implements the interface the Service defines
4. Model is imported by every layer but knows nothing about any of them

**Benefits:**
- ✅ Easy to test (mock the Repository interface in service tests)
- ✅ Easy to swap implementations (swap GORM for a different database)
- ✅ Clear responsibility separation
- ✅ No circular dependencies

**Key File References:**
- Handler example: `internal/handler/tesla_auth_handler.go`
- Service example: `internal/service/tesla_auth_service.go`
- Repository example: `internal/repository/tesla_repository.go`
- Model example: `internal/model/tesla_vehicle.go`

---

### Q: What's Clean Architecture and why does TeslaGo use it?

**Short Answer:**
Clean Architecture is a design pattern that keeps the innermost layers (business logic, models) independent from outer layers (frameworks, databases). TeslaGo uses it for flexibility and testability.

**Why It Matters:**
- Business logic (Service layer) doesn't know about GORM, HTTP, or PostgreSQL
- You can swap databases without rewriting business logic
- You can test services without a database
- Code is easier to understand and maintain

**Key File Reference:**
- AGENTS.md explains the architecture: Section "Architecture Layers"

---

## Testing

### Q: How does TeslaGo approach testing?

**Short Answer:**
BDD-style testing with Ginkgo + Gomega. Dependencies are mocked. No database required for unit tests.

**Test Layers:**
- **Unit tests** — Test services with mocked repositories
- **Integration tests** — Test against a real test database (not currently in TeslaGo)
- **Handler tests** — Test HTTP parsing and response formatting with mocked services

**Key Commands:**
```bash
# Run all tests
go test ./...

# Run with Ginkgo (BDD style)
ginkgo -r

# Run specific package
go test -v ./internal/service/...
```

**Framework:**
- **Ginkgo** — BDD test structure (`Describe`, `Context`, `It`)
- **Gomega** — Assertions (`Expect`, `To`, `Equal`)

---

## Go Language Concepts

### Q: What does `interface` mean in Go?

**Short Answer:**
An interface is a contract defining what methods a type must implement. It's how Go enables loose coupling and dependency injection.

**Example from TeslaGo:**
```go
// The interface (what the service needs)
type TeslaRepository interface {
    UpsertTeslaUser(ctx context.Context, user *model.TeslaUser) error
    GetTeslaUserByAdminID(ctx context.Context, adminID string) (*model.TeslaUser, error)
}

// The implementation (what the repository provides)
type teslaRepository struct {
    db *gorm.DB
}

func (r *teslaRepository) UpsertTeslaUser(ctx context.Context, user *model.TeslaUser) error {
    // implementation
}

// In tests, we can mock it
type mockRepository struct {}
func (m *mockRepository) UpsertTeslaUser(ctx context.Context, user *model.TeslaUser) error {
    // test implementation
}
```

**Why It Matters:**
- Service doesn't know about GORM or PostgreSQL (just the interface)
- Tests can pass a mock repository instead of the real one
- Easy to swap implementations

---

## Configuration & Deployment

### Q: How does configuration work in TeslaGo?

**Short Answer:**
All configuration comes from environment variables. No config files. This follows 12-Factor App principles.

**Files:**
- `.env` — Local development (git-ignored)
- `internal/config/config.go` — Loads env vars into a struct
- `cmd/api/main.go` — Uses the config to set up the app

**Usage:**
```go
cfg := config.LoadConfig()  // Reads environment variables
db, err := database.Connect(cfg)  // Uses config
```

**Key Reference:**
- README.md Section: "Environment Variables"

---

## Future Categories

Add new sections as you explore:
- [ ] Error Handling
- [ ] Authentication & Authorization
- [ ] External APIs (Tesla)
- [ ] Concurrency & Goroutines
- [ ] Performance & Optimization
- [ ] Docker & Deployment
- [ ] HTTP & REST API Design

---
