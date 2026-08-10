-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

-- These four tables carry FORCE ROW LEVEL SECURITY with an audience SELECT
-- policy and nothing else, so every INSERT, UPDATE and DELETE is denied — the
-- same defect that stopped Agent runs from being queued in 20261012. Every
-- writer reaches them through the service-mode Space transaction, which already
-- enforces membership and permissions in the application layer, so the write
-- policies are service-scoped. The audience SELECT policies are deliberately
-- narrower than Space membership and are left exactly as they are.
CREATE POLICY space_action_suggestion_batches_service_write ON space_action_suggestion_batches
    FOR INSERT WITH CHECK (misty_rls_is_service());
CREATE POLICY space_action_suggestion_batches_service_update ON space_action_suggestion_batches
    FOR UPDATE USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY space_action_suggestion_batches_service_delete ON space_action_suggestion_batches
    FOR DELETE USING (misty_rls_is_service());

CREATE POLICY space_action_suggestion_items_service_write ON space_action_suggestion_items
    FOR INSERT WITH CHECK (misty_rls_is_service());
CREATE POLICY space_action_suggestion_items_service_update ON space_action_suggestion_items
    FOR UPDATE USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY space_action_suggestion_items_service_delete ON space_action_suggestion_items
    FOR DELETE USING (misty_rls_is_service());

CREATE POLICY space_conversation_follow_ups_service_write ON space_conversation_follow_ups
    FOR INSERT WITH CHECK (misty_rls_is_service());
CREATE POLICY space_conversation_follow_ups_service_update ON space_conversation_follow_ups
    FOR UPDATE USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY space_conversation_follow_ups_service_delete ON space_conversation_follow_ups
    FOR DELETE USING (misty_rls_is_service());

CREATE POLICY space_conversation_follow_up_recipients_service_write ON space_conversation_follow_up_recipients
    FOR INSERT WITH CHECK (misty_rls_is_service());
CREATE POLICY space_conversation_follow_up_recipients_service_update ON space_conversation_follow_up_recipients
    FOR UPDATE USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY space_conversation_follow_up_recipients_service_delete ON space_conversation_follow_up_recipients
    FOR DELETE USING (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

DROP POLICY IF EXISTS space_action_suggestion_batches_service_write ON space_action_suggestion_batches;
DROP POLICY IF EXISTS space_action_suggestion_batches_service_update ON space_action_suggestion_batches;
DROP POLICY IF EXISTS space_action_suggestion_batches_service_delete ON space_action_suggestion_batches;
DROP POLICY IF EXISTS space_action_suggestion_items_service_write ON space_action_suggestion_items;
DROP POLICY IF EXISTS space_action_suggestion_items_service_update ON space_action_suggestion_items;
DROP POLICY IF EXISTS space_action_suggestion_items_service_delete ON space_action_suggestion_items;
DROP POLICY IF EXISTS space_conversation_follow_ups_service_write ON space_conversation_follow_ups;
DROP POLICY IF EXISTS space_conversation_follow_ups_service_update ON space_conversation_follow_ups;
DROP POLICY IF EXISTS space_conversation_follow_ups_service_delete ON space_conversation_follow_ups;
DROP POLICY IF EXISTS space_conversation_follow_up_recipients_service_write ON space_conversation_follow_up_recipients;
DROP POLICY IF EXISTS space_conversation_follow_up_recipients_service_update ON space_conversation_follow_up_recipients;
DROP POLICY IF EXISTS space_conversation_follow_up_recipients_service_delete ON space_conversation_follow_up_recipients;
-- +goose StatementEnd
