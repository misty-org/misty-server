-- +goose Up
-- Private native callback receipt. Existing Go events remain NULL and cannot be
-- mistaken for a verified native state transition during ownership handover.
ALTER TABLE ai_invocation_events ADD COLUMN native_resulting_state TEXT
  CHECK(native_resulting_state IN ('running','awaiting_approval','completed','failed','canceled'));
-- +goose Down
-- Forward only: preserve replay/conflict evidence across application rollback.
SELECT 1;
