-- +goose Up
-- +goose StatementBegin
-- Earlier pre-release copies of the Figma migration allowed every Space member
-- to mutate bindings, subscriptions, and normalized provider records through
-- RLS. Recreate the policies under new least-privilege rules so databases that
-- already applied that migration are hardened without rewriting migration
-- history.
DROP POLICY IF EXISTS figma_space_bindings_member ON figma_space_bindings;
DROP POLICY IF EXISTS figma_space_bindings_member_read ON figma_space_bindings;
DROP POLICY IF EXISTS figma_space_bindings_service_write ON figma_space_bindings;
CREATE POLICY figma_space_bindings_member_read ON figma_space_bindings
    FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY figma_space_bindings_service_write ON figma_space_bindings
    FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DROP POLICY IF EXISTS figma_webhook_subscriptions_member ON figma_webhook_subscriptions;
DROP POLICY IF EXISTS figma_webhook_subscriptions_member_read ON figma_webhook_subscriptions;
DROP POLICY IF EXISTS figma_webhook_subscriptions_service_write ON figma_webhook_subscriptions;
CREATE POLICY figma_webhook_subscriptions_member_read ON figma_webhook_subscriptions
    FOR SELECT USING (
        misty_rls_is_service() OR EXISTS (
            SELECT 1 FROM figma_space_bindings binding
            WHERE binding.id=figma_webhook_subscriptions.binding_id
              AND misty_is_space_member(binding.space_id)
        )
    );
CREATE POLICY figma_webhook_subscriptions_service_write ON figma_webhook_subscriptions
    FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DROP POLICY IF EXISTS figma_content_records_member ON figma_content_records;
DROP POLICY IF EXISTS figma_content_records_member_read ON figma_content_records;
DROP POLICY IF EXISTS figma_content_records_service_write ON figma_content_records;
CREATE POLICY figma_content_records_member_read ON figma_content_records
    FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY figma_content_records_service_write ON figma_content_records
    FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DROP POLICY IF EXISTS figma_webhook_deliveries_service ON figma_webhook_deliveries;
CREATE POLICY figma_webhook_deliveries_service ON figma_webhook_deliveries
    FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DROP POLICY IF EXISTS figma_comment_audit_member_read ON figma_comment_audit;
DROP POLICY IF EXISTS figma_comment_audit_service_write ON figma_comment_audit;
CREATE POLICY figma_comment_audit_member_read ON figma_comment_audit
    FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY figma_comment_audit_service_write ON figma_comment_audit
    FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only security hardening: rollback must not restore member write
-- access to provider bindings, webhook secrets, or normalized records.
SELECT 1;
-- +goose StatementEnd
