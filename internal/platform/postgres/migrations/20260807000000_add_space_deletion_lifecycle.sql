-- +goose Up
-- +goose StatementBegin
ALTER TABLE spaces ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active' CHECK (lifecycle_state IN ('active','pending_deletion','deleted'));
ALTER TABLE spaces ADD COLUMN deletion_requested_at TIMESTAMPTZ;
ALTER TABLE spaces ADD COLUMN permanent_delete_after TIMESTAMPTZ;
CREATE INDEX spaces_lifecycle_owner_idx ON spaces(owner_user_id,lifecycle_state,created_at);
CREATE OR REPLACE FUNCTION misty_is_space_member(candidate_space_id TEXT)
RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$
    SELECT EXISTS(SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id WHERE m.space_id=candidate_space_id AND m.user_id=misty_rls_user_id() AND s.lifecycle_state='active')
$$;
CREATE OR REPLACE FUNCTION misty_is_space_owner(candidate_space_id TEXT)
RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$
    SELECT EXISTS(SELECT 1 FROM spaces WHERE id=candidate_space_id AND owner_user_id=misty_rls_user_id() AND lifecycle_state='active')
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS spaces_lifecycle_owner_idx;
CREATE OR REPLACE FUNCTION misty_is_space_member(candidate_space_id TEXT)
RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off AS $$
    SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=candidate_space_id AND user_id=misty_rls_user_id())
$$;
CREATE OR REPLACE FUNCTION misty_is_space_owner(candidate_space_id TEXT)
RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off AS $$
    SELECT EXISTS(SELECT 1 FROM spaces WHERE id=candidate_space_id AND owner_user_id=misty_rls_user_id())
$$;
ALTER TABLE spaces DROP COLUMN IF EXISTS permanent_delete_after,DROP COLUMN IF EXISTS deletion_requested_at,DROP COLUMN IF EXISTS lifecycle_state;
-- +goose StatementEnd
