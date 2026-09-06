-- +goose Up
ALTER TABLE hosted_ai_reservations ADD COLUMN lease_expires_at TIMESTAMPTZ;
CREATE INDEX hosted_ai_reservations_lease ON hosted_ai_reservations(lease_expires_at) WHERE status='reserved';

-- +goose Down
DROP INDEX hosted_ai_reservations_lease;
ALTER TABLE hosted_ai_reservations DROP COLUMN lease_expires_at;
