-- +goose Up
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';

-- The original SECURITY DEFINER helpers forced row_security=off. That works
-- only when their owner has BYPASSRLS; when migrations are run by a normal
-- non-bypass role against FORCE ROW LEVEL SECURITY tables, PostgreSQL rejects
-- the lookup instead of returning a filtered result. Keep the base policies
-- non-recursive, then let the helpers evaluate with RLS enabled so they work
-- for both hardened production roles and local/test database owners.
DROP POLICY IF EXISTS spaces_read ON spaces;
DROP POLICY IF EXISTS spaces_owner_write ON spaces;
CREATE POLICY spaces_read ON spaces FOR SELECT
USING (
    misty_rls_is_service()
    OR owner_user_id=misty_rls_user_id()
    OR EXISTS (
        SELECT 1 FROM space_members member
        WHERE member.space_id=spaces.id AND member.user_id=misty_rls_user_id()
    )
);
CREATE POLICY spaces_owner_write ON spaces FOR ALL
USING (misty_rls_is_service())
WITH CHECK (misty_rls_is_service());

DROP POLICY IF EXISTS space_members_read ON space_members;
DROP POLICY IF EXISTS space_members_owner_write ON space_members;
CREATE POLICY space_members_read ON space_members FOR SELECT
USING (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY space_members_owner_write ON space_members FOR ALL
USING (misty_rls_is_service())
WITH CHECK (misty_rls_is_service());

CREATE OR REPLACE FUNCTION misty_is_space_member(candidate_space_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = on
AS $$
    SELECT EXISTS (
        SELECT 1 FROM space_members
        WHERE space_id=candidate_space_id AND user_id=misty_rls_user_id()
    )
$$;

CREATE OR REPLACE FUNCTION misty_is_space_owner(candidate_space_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = on
AS $$
    SELECT EXISTS (
        SELECT 1 FROM spaces
        WHERE id=candidate_space_id AND owner_user_id=misty_rls_user_id()
    )
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';

CREATE OR REPLACE FUNCTION misty_is_space_member(candidate_space_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT EXISTS (
        SELECT 1 FROM space_members
        WHERE space_id=candidate_space_id AND user_id=misty_rls_user_id()
    )
$$;

CREATE OR REPLACE FUNCTION misty_is_space_owner(candidate_space_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT EXISTS (
        SELECT 1 FROM spaces
        WHERE id=candidate_space_id AND owner_user_id=misty_rls_user_id()
    )
$$;

DROP POLICY IF EXISTS spaces_read ON spaces;
DROP POLICY IF EXISTS spaces_owner_write ON spaces;
CREATE POLICY spaces_read ON spaces FOR SELECT
USING (misty_rls_is_service() OR misty_is_space_member(id));
CREATE POLICY spaces_owner_write ON spaces FOR ALL
USING (misty_rls_is_service() OR misty_is_space_owner(id))
WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

DROP POLICY IF EXISTS space_members_read ON space_members;
DROP POLICY IF EXISTS space_members_owner_write ON space_members;
CREATE POLICY space_members_read ON space_members FOR SELECT
USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_members_owner_write ON space_members FOR ALL
USING (misty_rls_is_service() OR misty_is_space_owner(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));
-- +goose StatementEnd
