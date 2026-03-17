// Package database handles the PostgreSQL connection setup for TeslaGo.
//
// This package has a single responsibility: take the configuration and return
// a ready-to-use *gorm.DB. Everything else (queries, migrations, models) lives
// in other packages.
//
// What is GORM?
// ─────────────
// GORM is an ORM (Object-Relational Mapper) for Go. Instead of writing raw SQL
// like "SELECT * FROM tesla_users WHERE admin_id = $1", you write Go code:
//
//	db.Where("admin_id = ?", adminID).First(&user)
//
// GORM translates that into the correct SQL for the database you're using
// (PostgreSQL in our case). It also handles:
//   - AutoMigrate: automatically creates/updates tables based on your model structs
//   - Connection pooling: reuses TCP connections for efficiency
//   - Context support: cancellable queries via context.Context
//
// What is AutoMigrate?
// ────────────────────
// db.AutoMigrate(&Model{}) inspects the Go struct and the current database schema,
// then runs ALTER TABLE / CREATE TABLE statements to make them match.
//
// IMPORTANT limitations of AutoMigrate:
//   - It only ADDS columns and creates tables — it never drops columns or tables.
//   - For destructive changes (renaming, dropping) you need manual SQL migrations.
//   - It is fine for development and small projects; large production systems
//     usually prefer explicit migration files (like the .sql files in /migrations).
package database

import (
	"fmt"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/tomyogms/TeslaGo/internal/config"
	"github.com/tomyogms/TeslaGo/internal/model"
)

// Connect opens a connection to PostgreSQL using the provided config, runs
// AutoMigrate to ensure all tables exist, and returns the *gorm.DB instance.
//
// The returned *gorm.DB is safe for concurrent use — GORM manages an internal
// connection pool. Pass this single instance to all repositories; do not create
// multiple connections.
//
// Connection string (DSN) fields:
//   - host=     → hostname of the DB server (e.g. "localhost" or Docker service name "db")
//   - user=     → PostgreSQL username
//   - password= → PostgreSQL password
//   - dbname=   → database to connect to
//   - port=     → TCP port (default 5432)
//   - sslmode=disable  → no TLS for local development. Enable for production.
//   - TimeZone=UTC     → all timestamps stored and returned in UTC to avoid confusion.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC",
		cfg.DBHost, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBPort,
	)

	// gorm.Open() creates the connection pool. It does NOT immediately open a
	// TCP connection — the first actual query does. The &gorm.Config{} can be
	// used to customise logging, naming conventions, etc.
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// AutoMigrate creates or updates tables for each model struct.
	// Order matters when there are foreign keys:
	//   TeslaUser must exist before TeslaVehicle (which references it).
	if err := db.AutoMigrate(
		&model.TeslaUser{},    // creates/updates the tesla_users table
		&model.TeslaVehicle{}, // creates/updates the tesla_vehicles table (FK → tesla_users)
	); err != nil {
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	log.Println("Database connection established")
	return db, nil
}
