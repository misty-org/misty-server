-- +goose Up
-- Provider-neutral delivery records. Existing billing and retired app data is
-- intentionally untouched until the separately rehearsed archival/cutover.
CREATE TABLE billing_adapter_outbox (
 id text PRIMARY KEY,
 action text NOT NULL CHECK(action IN ('settle','release','refund')),
 payload jsonb NOT NULL,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 delivered_at timestamptz
);
CREATE INDEX billing_adapter_outbox_due ON billing_adapter_outbox(available_at,created_at) WHERE delivered_at IS NULL;
CREATE TABLE billing_adapter_reservations (
 account_id text NOT NULL,
 key text NOT NULL,
 reservation_id text NOT NULL,
 admission jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,key)
);
-- +goose Down
-- Do not silently discard pending customer usage during rollback.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM billing_adapter_outbox WHERE delivered_at IS NULL) THEN
  RAISE EXCEPTION 'Drain billing adapter usage before reverting the outbox migration';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE billing_adapter_outbox;
DROP TABLE billing_adapter_reservations;
