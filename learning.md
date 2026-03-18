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
- [Go Package Management](#go-package-management)

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

## Go Package Management

### Q: What's the difference between `go.mod` and `go.sum`?

**Short Answer:**
`go.mod` lists your direct dependencies (what you intentionally imported). `go.sum` lists ALL dependencies (direct + transitive) with cryptographic hashes to verify integrity.

**Details:**

**go.mod — Direct Dependencies Only:**
```
module github.com/tomyogms/TeslaGo

go 1.26.1

require (
    github.com/gin-gonic/gin v1.12.0           ← You imported this
    github.com/joho/godotenv v1.5.1             ← You imported this
    github.com/onsi/ginkgo/v2 v2.28.1           ← You imported this
    github.com/onsi/gomega v1.39.1              ← You imported this
    gorm.io/driver/postgres v1.6.0              ← You imported this
    gorm.io/gorm v1.31.1                        ← You imported this
)
```

**go.sum — All Dependencies + Hashes:**
```
github.com/gin-gonic/gin v1.12.0 h1:b3YAbrZtnf8N//yjKeU2+MQsh2mY5htkZidOM7O0wG8=
github.com/gin-gonic/gin v1.12.0/go.mod h1:VxccKfsSllpKshkBWgVgRniFFAzFb9csfngsqANjnLc=
...
(156 total entries in TeslaGo's go.sum)
```

**What These Hashes Do:**
- Cryptographically verify each dependency's content hasn't been tampered with
- Protect against supply-chain attacks
- Required to be committed to git (prevents man-in-the-middle attacks)
- Go verifies checksums on every build against `go.sum`

**Key File References:**
- `go.mod`: 6 direct dependencies in TeslaGo
- `go.sum`: 156 total entries (6 direct + 150 transitive with dual hashes each)

---

### Q: What's the difference between `go.mod` and Python's requirements.txt or poetry.lock?

**Short Answer:**
Python's `requirements.txt` ≈ Go's `go.mod` (direct deps). Python's `poetry.lock` ≈ Go's `go.sum` (all deps + hashes). But Go makes `go.sum` mandatory for security.

**Comparison Table:**

| Feature | Python pip | Python Poetry | Go |
|---------|-----------|----------------|-----|
| **Direct dependencies file** | `requirements.txt` | `pyproject.toml` | `go.mod` |
| **All dependencies locked** | `pip freeze` output (manual) | `poetry.lock` (automatic) | `go.sum` (automatic & mandatory) |
| **Version ranges** | Yes (`^1.2`, `~1.2`) | Yes (`^1.2`, `~1.2`) | No (exact only: `v1.2.0`) |
| **Sub-dependency resolution** | Manual or via pip | Automatic (Poetry handles it) | Automatic (Go handles it) |
| **Checksum/Hash protection** | No (optional) | Yes (poetry.lock) | Yes (go.sum, **mandatory**) |
| **Must commit lock file?** | Optional | Recommended | **Required** |

**Key Insight:**
Python requires you to explicitly run `pip freeze` or use Poetry to manage transitive deps. Go does this automatically — you just commit `go.sum`.

---

### Q: Are sub-dependencies automatically included in go.mod or do I need to manage them?

**Short Answer:**
Automatically included. When you `go get` a package, Go recursively finds all its dependencies and adds them to `go.sum`. You only manage the packages YOU import.

**Example:**

When TeslaGo runs `go get github.com/gin-gonic/gin v1.12.0`:
```
go get downloads Gin, which depends on:
    ├─ github.com/bytedance/sonic (JSON library)
    ├─ github.com/go-playground/validator/v10 (validation)
    ├─ github.com/mattn/go-isatty (terminal detection)
    └─ ... (9 more packages)

All of these are automatically added to go.sum
None of them appear in go.mod's `require` block
```

**In go.mod:**
```
require (
    github.com/gin-gonic/gin v1.12.0  ← Only THIS is here
)
```

**In go.sum:**
```
github.com/gin-gonic/gin v1.12.0 h1:...
github.com/gin-gonic/gin v1.12.0/go.mod h1:...
github.com/bytedance/sonic v1.15.0 h1:...      ← Sub-dependency
github.com/bytedance/sonic v1.15.0/go.mod h1:... ← Sub-dependency
...
(all transitive dependencies are here)
```

**Why This Works:**
Go uses **Minimal Version Selection (MVS)** — automatically picks the highest compatible version needed. No dependency hell.

**Key File References:**
- `go.mod` (lines 5-12): Only 6 direct dependencies listed
- `go.sum` (156 lines): All transitive dependencies with hashes

---

### Q: How do I check what sub-dependencies a package brings in?

**Short Answer:**
Use `go mod graph` to see the full dependency tree, or `go list -m all` to list everything.

**Commands:**

```bash
# Show dependency tree (what depends on what)
go mod graph

# Output example:
# github.com/tomyogms/TeslaGo github.com/gin-gonic/gin@v1.12.0
# github.com/gin-gonic/gin@v1.12.0 github.com/bytedance/sonic@v1.15.0
# github.com/gin-gonic/gin@v1.12.0 github.com/go-playground/validator/v10@v10.30.1
# ... (many more)
```

```bash
# List all dependencies (flat list)
go list -m all

# Output example:
# github.com/tomyogms/TeslaGo
# github.com/Masterminds/semver/v3 v3.4.0
# github.com/bytedance/gopkg v0.1.3
# github.com/bytedance/sonic v1.15.0
# ... (156 entries in TeslaGo)
```

```bash
# Show why a specific module is needed
go mod why github.com/bytedance/sonic

# Output example:
# github.com/tomyogms/TeslaGo
# github.com/gin-gonic/gin@v1.12.0
# github.com/bytedance/sonic@v1.15.0
```

---

### Q: What happens if a sub-dependency has a vulnerability?

**Short Answer:**
You upgrade the direct dependency that brought it in. Go will automatically upgrade the sub-dependency to a safe version.

**Real-World Scenario:**

Suppose `github.com/bytedance/sonic v1.15.0` (a sub-dependency of Gin) has a security vulnerability:

**Step 1: Check for updates**
```bash
go list -m -u github.com/gin-gonic/gin
# Output: github.com/gin-gonic/gin v1.12.0 [v1.13.0]  ← Newer version available
```

**Step 2: Upgrade Gin (the direct dependency)**
```bash
go get -u github.com/gin-gonic/gin
```

Gin v1.13.0 might depend on `bytedance/sonic v1.15.1` (patched version). Go automatically resolves this.

**Step 3: Run tests**
```bash
go test ./...
```

**What if the newer version breaks your code?**
You have two options:

**Option A: Fix your code to work with the new version**
```bash
go get github.com/gin-gonic/gin@v1.13.0
# Fix your code, then test
go test ./...
```

**Option B: Manually upgrade just the sub-dependency (if possible)**
```bash
# This is a workaround, not recommended:
go get github.com/bytedance/sonic@v1.15.1
go mod tidy
```

**Best Practice:** Always upgrade the direct dependency, not the sub-dependency directly.

---

### Q: What if two sub-dependencies want different versions of the same package?

**Short Answer:**
Go's Minimal Version Selection (MVS) picks the highest compatible version. It rarely conflicts, but if it does, you must upgrade one of the direct dependencies.

**Example Scenario:**

```
TeslaGo requires:
├─ Gin v1.12.0
│  └─ depends on: Sonic >= v1.15.0
└─ SomeOtherLib v2.0.0
   └─ depends on: Sonic >= v1.16.0

Result: Go picks Sonic v1.16.0 (highest compatible version)
Both Gin and SomeOtherLib can use it (since they accept >= requirements)
```

**What if they're incompatible?**

```
TeslaGo requires:
├─ OldLib v1.0.0
│  └─ needs: JSON < v1.5.0
└─ NewLib v2.0.0
   └─ needs: JSON >= v2.0.0

Result: Conflict! You can't use both without upgrading one.
```

**Solution:** Upgrade `OldLib` to a newer version that supports `JSON >= v2.0.0`, or remove `NewLib`.

**Why This Rarely Happens in Go:**
- Module maintainers follow semantic versioning strictly
- Incompatibilities are rare because versions are pinned exactly (no `^` or `~`)
- Go's MVS algorithm is designed to avoid these conflicts

---

### Q: What's the difference between `require` and `indirect` in go.mod?

**Short Answer:**
`require` = you directly imported this. `indirect` = this is a sub-dependency (brought in by something you imported). They're just comments for readability.

**Example from TeslaGo's go.mod:**

```go
require (
    github.com/gin-gonic/gin v1.12.0       // ← Direct: you imported Gin
    github.com/joho/godotenv v1.5.1         // ← Direct: you imported godotenv
    gorm.io/gorm v1.31.1                    // ← Direct: you imported GORM
)

require (
    github.com/Masterminds/semver/v3 v3.4.0 // indirect
    github.com/bytedance/sonic v1.15.0      // indirect
    github.com/go-playground/validator/v10  // indirect
    // ... more indirect dependencies
)
```

**Why the Comments?**
- Helps developers understand what they directly imported
- `go mod tidy` adds the `// indirect` comment automatically
- Doesn't affect functionality — it's purely informational

**How Dependencies Get Marked as `indirect`:**
1. You run `go get github.com/gin-gonic/gin`
2. Gin depends on Sonic
3. Go adds Sonic to `go.sum` but marks it `// indirect` in `go.mod`
4. This tells you: "Your code doesn't import Sonic directly"

**Key File References:**
- `go.mod` (lines 5-12): First `require` block = direct dependencies
- `go.mod` (lines 14-59): Second `require` block = indirect dependencies with comments

---

### Q: How do I add a new dependency to TeslaGo?

**Short Answer:**
Import the package in your code, then run `go get .` or `go mod tidy` to add it to `go.mod` and `go.sum`.

**Step-by-Step Example:**

**Step 1: Import the package in your code**
```go
// internal/handler/example_handler.go
package handler

import (
    "github.com/some-org/some-package"  // ← Add this
)

func MyHandler() {
    some-package.DoSomething()
}
```

**Step 2: Download the dependency**
```bash
# Option A: Automatically discover and add all imports
go mod tidy

# Option B: Explicitly add a specific package
go get github.com/some-org/some-package
```

**Step 3: Verify it was added**
```bash
cat go.mod | grep some-package
# Output: require github.com/some-org/some-package v0.1.2

cat go.sum | grep some-package
# Output: 
# github.com/some-org/some-package v0.1.2 h1:abc...
# github.com/some-org/some-package v0.1.2/go.mod h1:xyz...
```

**Step 4: Run tests**
```bash
go test ./...
```

**Step 5: Commit both files**
```bash
git add go.mod go.sum
git commit -m "add: new-package v0.1.2 for [reason]"
```

**Best Practices:**
- Always commit both `go.mod` AND `go.sum`
- Don't manually edit `go.mod` or `go.sum` — let `go` commands do it
- Run `go mod tidy` before committing to remove unused dependencies

---

### Q: How do I upgrade a dependency?

**Short Answer:**
Use `go get -u package@version` to upgrade to a specific version, or `go get -u ./...` to upgrade all dependencies.

**Commands:**

**Upgrade to a specific version:**
```bash
go get github.com/gin-gonic/gin@v1.13.0
# Updates go.mod and go.sum
# Then run tests
go test ./...
```

**Upgrade to the latest available version:**
```bash
go get -u github.com/gin-gonic/gin
# Automatically picks @latest
```

**Upgrade all direct dependencies:**
```bash
go get -u ./...
# Updates all dependencies to their latest compatible versions
# Then run tests
go test ./...
```

**Check what versions are available before upgrading:**
```bash
go list -m -u github.com/gin-gonic/gin
# Output: github.com/gin-gonic/gin v1.12.0 [v1.13.0]
#                                           ↑ Latest available
```

**After upgrading:**
```bash
# Always run tests
go test ./...

# Verify changes
git diff go.mod go.sum
```

---

### Q: How do I downgrade a dependency?

**Short Answer:**
Use `go get package@version` to pin to an older version. Go treats downgrades the same as upgrades.

**Example: Downgrade Gin from v1.12.0 to v1.11.0**

```bash
go get github.com/gin-gonic/gin@v1.11.0
```

**Verify the downgrade:**
```bash
cat go.mod | grep gin-gonic/gin
# Output: require github.com/gin-gonic/gin v1.11.0
```

**Test after downgrading:**
```bash
go test ./...
```

**When You Might Downgrade:**
1. A newer version introduced a breaking change
2. A newer version has a bug (wait for a patch)
3. You need to be compatible with older systems

**Warning:** Avoid unnecessary downgrades — they prevent security updates.

---

### Q: Is go.sum checked into version control? Should it be?

**Short Answer:**
Yes, **absolutely commit `go.sum` to git**. It's mandatory for security. Without it, Go can't verify that downloaded packages haven't been tampered with.

**Why `go.sum` Must Be Committed:**

**Scenario 1: Without go.sum committed**
```
Developer A: Commits code with new dependency
              (didn't commit go.sum by mistake)

Developer B: Runs go mod tidy
              Go downloads the dependency from proxy
              Different version hash! ⚠️ Inconsistent state
```

**Scenario 2: With go.sum committed**
```
Developer A: Commits code with new dependency
              (committed go.mod and go.sum)

Developer B: Runs go mod tidy
              Go verifies that the checksum in go.sum matches
              ✅ Guaranteed to be the same package
```

**Security Aspect:**
```bash
# When you build, Go checks:
# "Is the downloaded package hash in go.sum?"
# If NOT, build fails with error:
# "go: verifying module: checksum mismatch"

This protects against:
- Compromised mirrors
- Man-in-the-middle attacks
- Supply-chain attacks
```

**File Size Note:**
- `go.mod`: ~2 KB (small)
- `go.sum`: ~5-10 KB per 50 dependencies (very small)

**So your .gitignore should NOT have:**
```bash
# ❌ WRONG - Don't do this
go.sum          # Never gitignore this!
```

**Correct .gitignore:**
```bash
# ✅ Correct
# go.mod and go.sum are committed
# Only ignore build artifacts and vendor directory if you use it
/bin/
/dist/
# Vendor is optional but if used, commit it too
```

---

### Q: Real-World Problem: A dependency needs a security patch but breaks my code

**Short Answer:**
Upgrade the dependency, run your tests to identify breaking changes, then fix your code. This is the only secure approach.

**Scenario:**

```
Current state:
├─ Your code uses: github.com/some-package v1.5.0
└─ A vulnerability is discovered in v1.5.0
   Latest patch: v1.5.1 (fixes vulnerability)
   
But the patch includes a breaking API change!
Your code compiled with v1.5.0 breaks with v1.5.1
```

**Step-by-Step Solution:**

**Step 1: Upgrade to the patched version**
```bash
go get github.com/some-package@v1.5.1
```

**Step 2: Run your tests (they will fail)**
```bash
go test ./...
# Error: undefined: SomeOldFunction
```

**Step 3: Fix your code**
```go
// Before (v1.5.0 API)
result := some-package.OldFunction()

// After (v1.5.1 API)
result := some-package.NewFunction()
```

**Step 4: Run tests again (they pass)**
```bash
go test ./...
# ✅ All tests pass
```

**Step 5: Commit**
```bash
git add go.mod go.sum internal/...
git commit -m "chore: upgrade some-package to v1.5.1 for security patch"
```

**What NOT to do:**

❌ **DON'T ignore the vulnerability:**
```bash
# Tempting but dangerous!
# Your code stays vulnerable and so do your users
```

❌ **DON'T manually edit go.sum:**
```bash
# This breaks Go's verification mechanism
# Never edit go.sum by hand
```

❌ **DON'T downgrade to the old version:**
```bash
go get github.com/some-package@v1.5.0  # ❌ No! This is insecure
```

❌ **DON'T use `replace` to avoid the fix:**
```go
// go.mod
replace github.com/some-package => github.com/some-package v1.4.0  // ❌ No!
```

**Best Practice:**
Keep dependencies updated. Run `go list -m -u all` regularly to check for updates.

---

### Q: Can I lock all dependencies to exact versions like Python?

**Short Answer:**
Yes, Go already does this by default. Every dependency is pinned to an exact version (e.g., `v1.12.0`, not `^1.12` or `~1.12`).

**How Go Does This:**

**Python (version ranges):**
```
# requirements.txt
flask>=2.0.0,<3.0.0   ← Range, could be 2.1, 2.5, etc.
```

**Go (exact versions only):**
```go
// go.mod
require github.com/gin-gonic/gin v1.12.0   ← Exact version always
```

**Why Exact Versions?**
- No surprises when other developers run `go mod download`
- Reproducible builds
- Easier to audit what changed

**So TeslaGo already has perfect reproducibility:**
```bash
# Any developer running this on any machine gets the SAME versions
go mod download
go build ./cmd/api
```

**If You Need Even More Control: Vendoring**

Optional vendoring (commit all dependency source code):
```bash
go mod vendor
# Creates /vendor/ directory with all source code
# Commit this to git for offline builds
git add vendor/
```

**But this is overkill for most projects** — `go.sum` already provides all the security and reproducibility you need.

---

### Summary Table: Go vs Python Dependency Management

| Aspect | Python pip | Python Poetry | Go |
|--------|-----------|----------------|-----|
| **Direct deps file** | `requirements.txt` | `pyproject.toml` | `go.mod` |
| **Lock file** | `pip freeze` (manual) | `poetry.lock` (auto) | `go.sum` (auto + mandatory) |
| **Version format** | Ranges (`^1.2`) | Ranges (`^1.2`) | Exact only (`v1.2.0`) |
| **Sub-deps automatic?** | No (must freeze) | Yes | Yes |
| **Conflict resolution** | Manual or pip | Poetry | Minimal Version Selection (MVS) |
| **Security hashes** | Optional | Yes | Yes (mandatory) |
| **Reproducible builds** | No (unless poetry.lock) | Yes | Yes (built-in) |
| **Command to add dep** | `pip install pkg` | `poetry add pkg` | `go get pkg` |
| **Command to lock** | `pip freeze` | `poetry lock` | `go mod tidy` |
| **Transitive deps visible** | After freeze | In poetry.lock | In go.sum |
| **Commit lock file?** | Optional | Recommended | **Required** |

**Key Takeaway:**
Go's approach is actually simpler than Python's — exact versions by default, mandatory security hashes, automatic sub-dependency resolution, and always reproducible.

---

## Future Categories

Add new sections as you explore:
- [x] Go Package Management ✅
- [ ] Error Handling
- [ ] Authentication & Authorization
- [ ] External APIs (Tesla)
- [ ] Concurrency & Goroutines
- [ ] Performance & Optimization
- [ ] Docker & Deployment
- [ ] HTTP & REST API Design

---
