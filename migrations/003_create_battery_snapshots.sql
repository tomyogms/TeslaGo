-- Migration 003: Create battery_snapshots table
--
-- Purpose:
--   Store point-in-time battery readings polled from the Tesla Owner API.
--   Because Tesla provides no history endpoint, we build our own history
--   by saving a row every time a caller hits GET /tesla/vehicles/{id}/battery.
--
-- Relationship:
--   battery_snapshots.vehicle_id → tesla_vehicles.id  (many snapshots per vehicle)
--
-- Retention:
--   Rows older than 90 days are removed by a scheduled cleanup job.
--   The index on (vehicle_id, snapshot_at) also serves range-delete queries.
--
-- Note: This migration is a reference file. TeslaGo uses GORM AutoMigrate in
-- development (see internal/database/database.go). Run this SQL manually in
-- production environments that manage schema changes explicitly.

CREATE TABLE IF NOT EXISTS battery_snapshots (
    -- Internal primary key, auto-incremented by PostgreSQL.
    id                    BIGSERIAL PRIMARY KEY,

    -- Foreign key to tesla_vehicles. Cascade delete: if a vehicle row is
    -- removed all its snapshots are automatically removed too.
    vehicle_id            BIGINT      NOT NULL REFERENCES tesla_vehicles(id) ON DELETE CASCADE,

    -- The UTC timestamp when this battery reading was captured.
    snapshot_at           TIMESTAMPTZ NOT NULL,

    -- State of charge as an integer percentage (0–100).
    battery_level         INT         NOT NULL,

    -- Estimated remaining range in miles.
    battery_range         DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Tesla charging state: 'Charging' | 'Complete' | 'Disconnected' | 'Stopped'
    charging_state        VARCHAR(32) NOT NULL DEFAULT '',

    -- Current charging speed (miles of range added per hour).
    charge_rate           DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Actual charger voltage (Volts). 0 when not charging.
    charger_voltage       INT         NOT NULL DEFAULT 0,

    -- Actual charger current (Amps). 0 when not charging.
    charger_actual_current INT        NOT NULL DEFAULT 0,

    -- Driver-configured target charge percentage.
    charge_limit_soc      INT         NOT NULL DEFAULT 0,

    -- Estimated hours remaining to reach charge_limit_soc.
    time_to_full_charge   DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Total kWh added in the current/most-recent charging session.
    charge_energy_added   DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Managed by GORM / application; not auto-set by PostgreSQL triggers.
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Composite index optimises two common query patterns:
--   1. "All snapshots for vehicle X" → WHERE vehicle_id = ?
--   2. "Snapshots for vehicle X in time range" → WHERE vehicle_id = ? AND snapshot_at BETWEEN ? AND ?
--   3. "Latest snapshot for vehicle X" → ORDER BY snapshot_at DESC LIMIT 1
CREATE INDEX IF NOT EXISTS idx_battery_snapshots_vehicle_time
    ON battery_snapshots (vehicle_id, snapshot_at DESC);
