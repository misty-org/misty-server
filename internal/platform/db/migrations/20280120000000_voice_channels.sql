-- +goose Up
-- A Space's voice channels: a standard channel people drop into for a call (LiveKit room
-- "space:<space>:voice:<channel>"), with the same roles, overwrites and read access as any
-- channel. The call itself never passes through the server.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_conversations
  ADD COLUMN voice boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT space_conversations_voice_kind_check CHECK (NOT voice OR kind = 'standard');

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
ALTER TABLE space_conversations
  DROP CONSTRAINT IF EXISTS space_conversations_voice_kind_check,
  DROP COLUMN IF EXISTS voice;
