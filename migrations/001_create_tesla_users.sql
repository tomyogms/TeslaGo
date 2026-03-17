-- +migrate Up
CREATE TABLE IF NOT EXISTS tesla_users (
    id          SERIAL PRIMARY KEY,
    admin_id    VARCHAR(255) NOT NULL UNIQUE,
    access_token  TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    token_expires_at TIMESTAMP NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP NOT NULL DEFAULT NOW()
);

-- +migrate Down
DROP TABLE IF EXISTS tesla_users;
