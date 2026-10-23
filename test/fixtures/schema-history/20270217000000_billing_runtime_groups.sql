-- +goose Up
ALTER TABLE billing_adapter_outbox DROP CONSTRAINT billing_adapter_outbox_action_check;
ALTER TABLE billing_adapter_outbox ADD CONSTRAINT billing_adapter_outbox_action_check
 CHECK(action IN ('settle','release','refund','settle_group','release_group'));
CREATE INDEX billing_adapter_reservations_operation ON billing_adapter_reservations(account_id,(admission->>'operation_id'));
-- +goose Down
-- Pending group completions must not be silently discarded during rollback.
ALTER TABLE billing_adapter_outbox DROP CONSTRAINT billing_adapter_outbox_action_check;
ALTER TABLE billing_adapter_outbox ADD CONSTRAINT billing_adapter_outbox_action_check CHECK(action IN ('settle','release','refund'));
DROP INDEX billing_adapter_reservations_operation;
