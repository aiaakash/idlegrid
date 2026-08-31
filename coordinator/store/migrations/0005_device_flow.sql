-- Phase 5: RFC 8628-style device authorization for provider onboarding.
-- Replaces the static --enroll-code with `idlegrid-provider login`: the CLI
-- requests a device code, the user approves it in the console (where their
-- email session already proves identity), and the CLI receives a provider
-- token it presents at WS register.

-- One row per login attempt. device_code is the CLI's polling secret (stored
-- hashed); user_code is the short human code typed into the console.
CREATE TABLE IF NOT EXISTS device_codes (
    device_code_hash TEXT PRIMARY KEY,
    user_code        TEXT UNIQUE NOT NULL,   -- canonical: 8 upper chars, no dash
    user_id          BIGINT REFERENCES users(id), -- set on approval
    expires_at       TIMESTAMPTZ NOT NULL,
    approved_at      TIMESTAMPTZ,
    consumed_at      TIMESTAMPTZ,            -- set when the token is issued
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_device_codes_user_code ON device_codes (user_code);

-- Provider auth tokens issued after device approval. The node presents the
-- token (hashed here) at register; revocable per token.
CREATE TABLE IF NOT EXISTS provider_tokens (
    token_hash  TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    label       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked     BOOLEAN NOT NULL DEFAULT false
);
