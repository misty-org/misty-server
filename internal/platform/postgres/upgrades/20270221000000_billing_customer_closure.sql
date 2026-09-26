-- +goose Up
ALTER TABLE billing_adapter_outbox DROP CONSTRAINT billing_adapter_outbox_action_check;
ALTER TABLE billing_adapter_outbox ADD CONSTRAINT billing_adapter_outbox_action_check
 CHECK(action IN ('settle','release','refund','settle_group','release_group','close'));
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM billing_adapter_outbox WHERE action='close') THEN
  RAISE EXCEPTION 'Preserve customer closure delivery across rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE billing_adapter_outbox DROP CONSTRAINT billing_adapter_outbox_action_check;
ALTER TABLE billing_adapter_outbox ADD CONSTRAINT billing_adapter_outbox_action_check
 CHECK(action IN ('settle','release','refund','settle_group','release_group'));
