-- +goose Up
-- +goose StatementBegin
-- The canonical-Space repair migration could encounter an existing canonical
-- Space and only repair the operator. Backfill every active non-operator now;
-- misty_ensure_default_space is idempotent and also attaches all operators to
-- each private support conversation.
DO $$
DECLARE
    candidate_user_id TEXT;
BEGIN
    FOR candidate_user_id IN
        SELECT u.id
        FROM users u
        WHERE u.lifecycle_state='active'
          AND NOT EXISTS(
              SELECT 1 FROM misty_space_operators o WHERE o.user_id=u.id
          )
        ORDER BY u.id
    LOOP
        PERFORM misty_ensure_default_space(candidate_user_id);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: support conversations may contain customer messages after
-- provisioning and must never be discarded by a rollback.
SELECT 1;
