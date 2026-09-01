-- Phase 6: provider node ownership hygiene.
-- Records WHICH provider token bound a node so that "remove this Mac" can
-- revoke the exact credential that would otherwise silently rebind it.
ALTER TABLE provider_nodes ADD COLUMN IF NOT EXISTS bound_with_token TEXT;
