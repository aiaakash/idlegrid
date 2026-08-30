-- Phase 1: billing foundation. Ledger/accounts/prices/payouts tables are
-- created now (forward-compatible) and populated from Phase 2 onward.

CREATE TABLE IF NOT EXISTS users (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL DEFAULT 'developer', -- admin | developer | provider_owner
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_keys (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id),
    key_hash   TEXT NOT NULL UNIQUE, -- sha256 hex of the full plaintext key
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS provider_nodes (
    node_id     TEXT PRIMARY KEY,
    user_id     BIGINT REFERENCES users(id), -- null until enrolled to an account
    enrolled_at TIMESTAMPTZ,
    last_seen   TIMESTAMPTZ,
    error_count INT NOT NULL DEFAULT 0
);

-- One row per request: the billing-grade metering record.
CREATE TABLE IF NOT EXISTS usage_events (
    id                        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id                TEXT NOT NULL UNIQUE,
    user_id                   BIGINT REFERENCES users(id), -- developer (null = admin)
    node_id                   TEXT,
    model                     TEXT NOT NULL,
    est_input_tokens          INT NOT NULL,
    est_output_tokens         INT NOT NULL,
    provider_input_tokens     INT,
    provider_output_tokens    INT,
    counts_within_tolerance   BOOLEAN,
    status                    TEXT NOT NULL, -- completed | failed | cancelled | timeout
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_usage_events_user ON usage_events (user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_events_node ON usage_events (node_id, created_at);

CREATE TABLE IF NOT EXISTS accounts (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id),
    kind       TEXT NOT NULL, -- developer_balance | provider_earnings | platform_revenue
    UNIQUE (user_id, kind)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id),
    entry_type  TEXT NOT NULL, -- deposit | usage_charge | provider_earning | platform_fee | payout | adjustment
    amount_micro BIGINT NOT NULL, -- signed; 1_000_000 micro = $1
    ref_type    TEXT NOT NULL, -- request | payment | payout | admin
    ref_id      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, entry_type, ref_type, ref_id)
);
CREATE INDEX IF NOT EXISTS idx_ledger_account ON ledger_entries (account_id, created_at);

CREATE TABLE IF NOT EXISTS model_prices (
    model_id            TEXT PRIMARY KEY,
    input_micro_per_1m  BIGINT NOT NULL,
    output_micro_per_1m BIGINT NOT NULL,
    active              BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS payouts (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id),
    amount_micro BIGINT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'requested', -- requested | approved | paid
    rail         TEXT NOT NULL DEFAULT 'manual',
    rail_ref     TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS dodo_payments (
    dodo_payment_id TEXT PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id),
    amount_micro    BIGINT NOT NULL,
    status          TEXT NOT NULL,
    raw             JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
