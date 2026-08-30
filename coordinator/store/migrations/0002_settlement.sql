-- Phase 2: settlement — where the money columns live on usage rows,
-- plus the platform account seed.

ALTER TABLE usage_events
    ADD COLUMN IF NOT EXISTS gross_micro            BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS provider_credit_micro  BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS platform_fee_micro     BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS settled                BOOLEAN NOT NULL DEFAULT false;

-- The platform (revenue) account needs a user to anchor the FK.
INSERT INTO users (email, role) VALUES ('platform@idlegrid.system', 'admin')
ON CONFLICT (email) DO NOTHING;
