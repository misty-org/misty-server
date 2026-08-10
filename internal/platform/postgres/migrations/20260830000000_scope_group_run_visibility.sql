-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_is_shared_space_run_visible(
    candidate_space_id TEXT,
    candidate_source_type TEXT,
    candidate_source_conversation_id TEXT
)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT CASE
        WHEN candidate_source_type='schedule' THEN misty_is_space_member(candidate_space_id)
        WHEN candidate_source_type='group_mention' AND EXISTS(
            SELECT 1 FROM space_conversations c
            WHERE c.id=candidate_source_conversation_id AND c.space_id=candidate_space_id
        ) THEN misty_is_space_conversation_member(candidate_source_conversation_id)
        WHEN candidate_source_type='group_mention' THEN misty_is_space_member(candidate_space_id)
        ELSE FALSE
    END
$$;

DROP POLICY space_runs_private_or_shared_policy ON space_runs;
CREATE POLICY space_runs_private_or_shared_policy ON space_runs FOR ALL
    USING (
        misty_rls_is_service() OR
        requesting_member_id=misty_rls_user_id() OR
        misty_is_shared_space_run_visible(space_id,source_type,source_conversation_id)
    )
    WITH CHECK (
        misty_rls_is_service() OR
        requesting_member_id=misty_rls_user_id() OR
        misty_is_shared_space_run_visible(space_id,source_type,source_conversation_id)
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY space_runs_private_or_shared_policy ON space_runs;
CREATE POLICY space_runs_private_or_shared_policy ON space_runs FOR ALL
    USING (
        misty_rls_is_service() OR
        (source_type='group_mention' AND misty_is_space_member(space_id)) OR
        requesting_member_id=misty_rls_user_id()
    )
    WITH CHECK (
        misty_rls_is_service() OR
        (source_type='group_mention' AND misty_is_space_member(space_id)) OR
        requesting_member_id=misty_rls_user_id()
    );
DROP FUNCTION IF EXISTS misty_is_shared_space_run_visible(TEXT,TEXT,TEXT);
-- +goose StatementEnd
