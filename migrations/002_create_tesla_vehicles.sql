-- +migrate Up
CREATE TABLE IF NOT EXISTS tesla_vehicles (
    id              SERIAL PRIMARY KEY,
    tesla_user_id   INT NOT NULL REFERENCES tesla_users(id) ON DELETE CASCADE,
    vehicle_id      BIGINT NOT NULL,
    display_name    VARCHAR(255),
    vin             VARCHAR(17),
    state           VARCHAR(50),
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(tesla_user_id, vehicle_id)
);

CREATE INDEX IF NOT EXISTS idx_tesla_vehicles_tesla_user_id ON tesla_vehicles(tesla_user_id);

-- +migrate Down
DROP TABLE IF EXISTS tesla_vehicles;
