-- Phase 4: money movement — provider enrollment + dodo deposits.

-- Each user gets an enrollment code their provider Macs present at register.
ALTER TABLE users ADD COLUMN IF NOT EXISTS enrollment_code TEXT UNIQUE;

-- Providers bind to the account that enrolled them.
CREATE TABLE IF NOT EXISTS provider_nodes (
    node_id     TEXT PRIMARY KEY,
    user_id     BIGINT REFERENCES users(id),
    enrolled_at TIMESTAMPTZ,
    last_seen   TIMESTAMPTZ,
    error_count INT NOT NULL DEFAULT 0
);

-- Escrow transfers to the owner's account at enrollment; settled rows for an
-- enrolled node credit the owner directly.
ALTER TABLE usage_events
    ADD COLUMN IF NOT EXISTS transferred BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_usage_events_node_settled ON usage_events (node_id, settled);
