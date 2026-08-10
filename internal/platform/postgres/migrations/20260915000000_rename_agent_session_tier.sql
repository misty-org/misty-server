-- +goose Up
-- +goose StatementBegin
-- Mika was removed as a concept. The persisted agent tier moves from the
-- pre-rename shape {"mikaTier": "mika-high"} to {"agentTier": "tier-high"}.
--
-- agent/persistence.go keeps reading "mikaTier" as a fallback, and
-- NormalizeAgentTier keeps accepting "mika-*" values, for one more release:
-- this migration and the binary rollout are not atomic, so a session written
-- by the old binary after this runs must still resolve to its real tier
-- instead of silently dropping to the lowest one.

-- Rows that already carry the current key only need the stale alias dropped.
UPDATE agent_conversations
SET state = state - 'mikaTier'
WHERE state ? 'mikaTier'
  AND state ? 'agentTier';

-- Rows holding only the legacy key are moved, rewriting the value prefix.
-- Guarded on jsonb_typeof so a null or malformed value cannot become the
-- string "null" under the new key.
UPDATE agent_conversations
SET state = (state - 'mikaTier')
    || jsonb_build_object('agentTier', to_jsonb(replace(state ->> 'mikaTier', 'mika-', 'tier-')))
WHERE state ? 'mikaTier'
  AND jsonb_typeof(state -> 'mikaTier') = 'string';

-- Anything left is a legacy key with a non-string value; it carries no tier
-- worth preserving, so drop it and let the loader apply its default.
UPDATE agent_conversations
SET state = state - 'mikaTier'
WHERE state ? 'mikaTier';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE agent_conversations
SET state = (state - 'agentTier')
    || jsonb_build_object('mikaTier', to_jsonb(replace(state ->> 'agentTier', 'tier-', 'mika-')))
WHERE state ? 'agentTier'
  AND jsonb_typeof(state -> 'agentTier') = 'string';
-- +goose StatementEnd
