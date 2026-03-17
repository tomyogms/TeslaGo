// Package config handles loading all application configuration from environment
// variables, following the 12-Factor App methodology (https://12factor.net/config).
//
// Why environment variables?
// ──────────────────────────
// Storing configuration in env vars (not in files committed to git) means:
//   - Secrets (passwords, API keys) are never accidentally committed to source control.
//   - The same Docker image can run in dev, staging, and production just by
//     changing env vars — no code changes needed.
//   - Config is clearly separated from code.
//
// How to provide values:
//   - Development: create a `.env` file in the project root (loaded by godotenv).
//     This file is git-ignored so secrets stay local.
//   - Production / Docker: set real environment variables in docker-compose.yaml,
//     Kubernetes secrets, or your cloud provider's secrets manager.
package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Config is the central struct that holds every configuration value TeslaGo needs.
// A single instance is created at startup (in main.go) and passed to all components
// that need it. This avoids scattered os.Getenv() calls throughout the codebase.
type Config struct {
	// ── Application ─────────────────────────────────────────────────────────
	// AppHost is the IP address the HTTP server binds to.
	// "0.0.0.0" means "listen on all network interfaces" (required for Docker).
	AppHost string

	// AppPort is the TCP port the HTTP server listens on.
	// Default: 8080
	AppPort string

	// ── Database (PostgreSQL) ────────────────────────────────────────────────
	DBHost     string // Hostname of the PostgreSQL server, e.g. "localhost" or "db" in Docker
	DBPort     string // PostgreSQL port, default "5432"
	DBUser     string // Database username
	DBPassword string // Database password — never commit this to git
	DBName     string // Database name, e.g. "teslago"

	// ── Tesla OAuth ──────────────────────────────────────────────────────────

	// TeslaClientID is your Tesla developer application's client ID.
	// For the unofficial "owner-api" approach, this is typically "ownerapi".
	// Register your app at https://developer.tesla.com to get an official one.
	TeslaClientID string

	// TeslaClientSecret is your Tesla app's client secret.
	// Not used in PKCE flows but required for some grant types.
	TeslaClientSecret string

	// TeslaRedirectURI is the callback URL that Tesla redirects to after the
	// admin approves access. It MUST match exactly what is registered in the
	// Tesla developer portal — any mismatch causes an auth error.
	// Example: "http://localhost:8080/tesla/auth/callback"
	TeslaRedirectURI string

	// TeslaAuthBaseURL is the base URL for Tesla's OAuth/OpenID Connect server.
	// Global: "https://auth.tesla.com"
	// China:  "https://auth.tesla.cn"
	TeslaAuthBaseURL string

	// TeslaAPIBaseURL is the base URL for Tesla's Owner (REST) API.
	// Global: "https://owner-api.teslamotors.com"
	// China:  "https://owner-api.vn.cloud.tesla.cn"
	TeslaAPIBaseURL string

	// TeslaTokenSecret is the secret key used to AES-256-GCM encrypt/decrypt
	// Tesla tokens before storing them in the database.
	// IMPORTANT: Use a long, random string (32+ chars). Losing this key means
	// all stored tokens become permanently unreadable.
	// Generate with: openssl rand -hex 32
	TeslaTokenSecret string
}

// LoadConfig reads all configuration from environment variables and returns
// a populated Config struct.
//
// It attempts to load a `.env` file first (useful for local development).
// If no `.env` file exists (e.g. in production) it silently continues —
// real environment variables are expected to already be set.
//
// Default values are provided for non-secret fields so the app can start
// in a basic local setup without setting every variable manually.
func LoadConfig() *Config {
	// godotenv.Load() reads key=value pairs from .env and sets them as env vars.
	// It does NOT overwrite variables that are already set in the environment —
	// real environment variables always win over .env values.
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	return &Config{
		AppHost:    getEnv("APP_HOST", "0.0.0.0"),
		AppPort:    getEnv("APP_PORT", "8080"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", "postgres"),
		DBName:     getEnv("DB_NAME", "teslago"),

		// Tesla fields have no defaults — they MUST be set by the operator.
		// An empty string will cause auth flows to fail with clear error messages.
		TeslaClientID:     getEnv("TESLA_CLIENT_ID", ""),
		TeslaClientSecret: getEnv("TESLA_CLIENT_SECRET", ""),
		TeslaRedirectURI:  getEnv("TESLA_REDIRECT_URI", "http://localhost:8080/tesla/auth/callback"),
		TeslaAuthBaseURL:  getEnv("TESLA_AUTH_BASE_URL", "https://auth.tesla.com"),
		TeslaAPIBaseURL:   getEnv("TESLA_API_BASE_URL", "https://owner-api.teslamotors.com"),
		TeslaTokenSecret:  getEnv("TESLA_TOKEN_SECRET", ""),
	}
}

// getEnv is a helper that reads an environment variable by key.
// If the variable is not set, it returns the provided fallback default value.
//
// os.LookupEnv returns (value, exists):
//   - exists=true  → the variable is set (even if it is an empty string)
//   - exists=false → the variable is not set at all → use fallback
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
