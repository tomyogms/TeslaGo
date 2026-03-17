// Package model defines the data structures (entities) used across TeslaGo.
//
// Models are pure data — they have no business logic and no knowledge of the
// database, HTTP, or any other infrastructure. They sit at the very centre of
// Clean Architecture and are imported by every other layer.
//
// TeslaUser represents an admin's linked Tesla account.
// The relationship is:
//
//	Admin (TeslaGo user) ──1:1──▶ TeslaUser ──1:N──▶ TeslaVehicle
package model

import "time"

// TeslaUser stores the OAuth credentials that allow TeslaGo to act on behalf
// of a Tesla account owner (admin).
//
// Security design:
//   - AccessToken and RefreshToken are NEVER stored in plain text.
//   - Before writing to the database, the service layer encrypts them with
//     AES-256-GCM using the TESLA_TOKEN_SECRET environment variable.
//   - The `json:"-"` tag ensures these fields are NEVER included in any JSON
//     response, even if a developer accidentally serialises a TeslaUser.
//
// GORM annotations:
//   - `gorm:"primaryKey;autoIncrement"` → standard auto-incrementing integer PK
//   - `gorm:"uniqueIndex;not null"`     → database-level unique constraint
//   - `gorm:"not null"`                 → NOT NULL column constraint
type TeslaUser struct {
	// ID is the internal primary key, auto-assigned by PostgreSQL.
	ID uint `gorm:"primaryKey;autoIncrement" json:"id"`

	// AdminID is the identifier of the TeslaGo admin who linked this account.
	// It must be unique — one admin can link exactly one Tesla account.
	// This is intentionally a string so it can hold a UUID, email, or username.
	AdminID string `gorm:"uniqueIndex;not null" json:"admin_id"`

	// AccessToken is the AES-256-GCM encrypted Tesla Bearer token.
	// Expires after ~8 hours. Use GetValidAccessToken() to get a valid one.
	// json:"-" means this field is EXCLUDED from all JSON marshalling.
	AccessToken string `gorm:"not null" json:"-"`

	// RefreshToken is the AES-256-GCM encrypted long-lived Tesla refresh token.
	// Used to obtain a new AccessToken when it expires.
	// json:"-" means this field is EXCLUDED from all JSON marshalling.
	RefreshToken string `gorm:"not null" json:"-"`

	// TokenExpiresAt is the UTC timestamp when the current AccessToken expires.
	// The service layer uses this to decide whether to refresh proactively
	// (5 minutes before actual expiry) rather than waiting for a 401 error.
	TokenExpiresAt time.Time `gorm:"not null" json:"token_expires_at"`

	// CreatedAt and UpdatedAt are managed automatically by GORM.
	// GORM sets CreatedAt on INSERT and UpdatedAt on every UPDATE.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
