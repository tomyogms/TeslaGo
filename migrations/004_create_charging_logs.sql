-- Migration 004: Create charging_logs table
--
-- Purpose:
--   Store one row per inferred charging session. Sessions are detected by the
--   service layer by watching charging_state transitions in battery_snapshots:
--     Disconnected / Stopped → Charging  (session starts)
--     Charging → Complete / Stopped / Disconnected  (session ends)
--
-- Why "inferred"?
--   Tesla's Owner API has no native charging history endpoint. We reconstruct
--   sessions from our own battery_snapshots poll history.
--
-- Relationship:
--   charging_logs.vehicle_id → tesla_vehicles.id  (many sessions per vehicle)
--
-- In-progress sessions:
--   When a session starts, a row is inserted with ended_at = NULL.
--   When it ends, that row is updated with ended_at, end_battery_level,
--   energy_added, and max_charge_rate.
--
-- Retention:
--   Rows older than 90 days (by started_at) are pruned alongside snapshots.
--
-- Note: Reference migration file — see database.go for AutoMigrate usage.

CREATE TABLE IF NOT EXISTS charging_logs (
    -- Internal primary key.
    id                  BIGSERIAL PRIMARY KEY,

    -- Foreign key to tesla_vehicles. Cascade delete removes sessions if the
    -- vehicle is de-linked.
    vehicle_id          BIGINT      NOT NULL REFERENCES tesla_vehicles(id) ON DELETE CASCADE,

    -- UTC timestamp of the first snapshot that showed ChargingState = "Charging".
    started_at          TIMESTAMPTZ NOT NULL,

    -- UTC timestamp when the session ended. NULL means still in progress.
    ended_at            TIMESTAMPTZ,

    -- Battery percentage at the start of the session.
    start_battery_level INT         NOT NULL DEFAULT 0,

    -- Battery percentage at the end of the session. 0 if still in progress.
    end_battery_level   INT         NOT NULL DEFAULT 0,

    -- Total kWh delivered during this session.
    energy_added        DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Driver's configured charge limit for this session.
    charge_limit        INT         NOT NULL DEFAULT 0,

    -- Peak charge rate (miles/hour) observed during the session.
    max_charge_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index on (vehicle_id, started_at) supports:
--   1. "All sessions for vehicle X"
--   2. "Sessions for vehicle X in date range" (the common query-param filter)
--   3. "Latest session for vehicle X" → ORDER BY started_at DESC LIMIT 1
CREATE INDEX IF NOT EXISTS idx_charging_logs_vehicle_started
    ON charging_logs (vehicle_id, started_at DESC);
