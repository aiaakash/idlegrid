-- Phase 3: console — passwords for users + server-side sessions.

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;

CREATE TABLE IF NOT EXISTS sessions (
    token_hash  TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);
