-- +goose Up
-- Sites agents may read but never act on (docs/agents/harness.md,
-- Phase 5): banking, crypto, payment and adult sites by default, plus the
-- account's own list. Devices enforce it before any input reaches a page.
ALTER TABLE ai_user_settings ADD COLUMN IF NOT EXISTS agent_block_sensitive boolean NOT NULL DEFAULT true;
ALTER TABLE ai_user_settings ADD COLUMN IF NOT EXISTS agent_blocked_sites text[] NOT NULL DEFAULT '{}'
    CHECK (cardinality(agent_blocked_sites) <= 200);

-- +goose Down
ALTER TABLE ai_user_settings DROP COLUMN IF EXISTS agent_blocked_sites;
ALTER TABLE ai_user_settings DROP COLUMN IF EXISTS agent_block_sensitive;
