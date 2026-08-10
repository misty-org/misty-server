-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

-- space_agent_message_triggers shipped with FORCE ROW LEVEL SECURITY but only a
-- SELECT policy, so every INSERT and UPDATE was denied outright — a policy that
-- does not cover the command is never consulted, not even for the service role.
-- Queueing an Agent run therefore failed with 42501 after the message had
-- already been stored, which surfaced as "Misty could not load this Space right
-- now" while the composer kept the sent text. Replace it with the same
-- member-scoped FOR ALL policy the other Space-owned tables use.
DROP POLICY IF EXISTS space_agent_message_triggers_member_policy ON space_agent_message_triggers;
CREATE POLICY space_agent_message_triggers_member_policy ON space_agent_message_triggers FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

DROP POLICY IF EXISTS space_agent_message_triggers_member_policy ON space_agent_message_triggers;
CREATE POLICY space_agent_message_triggers_member_policy ON space_agent_message_triggers FOR SELECT
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM space_members sm
        WHERE sm.space_id=space_agent_message_triggers.space_id
          AND sm.user_id=misty_rls_user_id()
    ));
-- +goose StatementEnd
