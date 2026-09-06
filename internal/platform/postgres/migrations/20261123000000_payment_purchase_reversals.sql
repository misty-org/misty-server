-- +goose Up
CREATE TABLE payment_purchase_reversals (
    purchase_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_id TEXT NOT NULL,
    event_id UUID NOT NULL REFERENCES payment_entitlement_inbox(event_id),
    reason TEXT NOT NULL CHECK(reason IN ('refunded','disputed')),
    reversed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE payment_purchase_reversals ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_purchase_reversals FORCE ROW LEVEL SECURITY;
CREATE POLICY payment_purchase_reversals_service ON payment_purchase_reversals
  USING(current_setting('app.rls_mode',true)='service')
  WITH CHECK(current_setting('app.rls_mode',true)='service');

-- +goose Down
DROP TABLE payment_purchase_reversals;
