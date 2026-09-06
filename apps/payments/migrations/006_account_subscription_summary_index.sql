-- +goose Up
-- Account summaries and canonical effective-subscription selection share this
-- order; avoid scanning every customer's historical subscriptions per request.
CREATE INDEX subscriptions_account_summary ON billing.subscriptions (
    user_id,
    (CASE WHEN status IN ('active','trialing') THEN 0 WHEN status='past_due' THEN 1 ELSE 2 END),
    updated_at DESC,
    stripe_subscription_id
);
