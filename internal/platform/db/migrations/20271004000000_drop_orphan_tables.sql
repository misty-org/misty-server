-- +goose Up
-- These tables have no readers or writers left in the server; they belonged to
-- retired Slack/Discord links, SDK contract versions, custom member roles,
-- per-note permissions and earlier job queues.
DROP TABLE IF EXISTS account_deletion_provider_resources;
DROP TABLE IF EXISTS ai_runtime_callback_receipts;
DROP TABLE IF EXISTS browser_sync_checkpoints;
DROP TABLE IF EXISTS connection_authorization_requests;
DROP TABLE IF EXISTS native_task_effects;
DROP TABLE IF EXISTS password_recovery_jobs;
DROP TABLE IF EXISTS sdk_capability_contract_versions;
DROP TABLE IF EXISTS space_discord_links;
DROP TABLE IF EXISTS space_invitation_delivery_jobs;
DROP TABLE IF EXISTS space_member_roles;
DROP TABLE IF EXISTS space_member_storage_usage;
DROP TABLE IF EXISTS space_note_permissions;
DROP TABLE IF EXISTS space_slack_links;
DROP FUNCTION IF EXISTS misty_note_permission_guard();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'drop_orphan_tables is irreversible; restore from a backup instead';
END
$$;
-- +goose StatementEnd
