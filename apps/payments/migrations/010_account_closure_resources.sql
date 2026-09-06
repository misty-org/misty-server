-- +goose Up
ALTER TABLE billing.account_closures
    ADD COLUMN available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0);
CREATE INDEX account_closures_due ON billing.account_closures(available_at,user_id) WHERE state='closing';
CREATE TABLE billing.account_closure_resources (
    kind TEXT NOT NULL CHECK(kind IN ('checkout','subscription','customer')),
    resource_id TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES billing.account_closures(user_id) ON DELETE CASCADE,
    customer_id TEXT,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed')),
    phase TEXT NOT NULL DEFAULT 'sessions' CHECK(phase IN ('sessions','subscriptions','delete')),
    cursor TEXT,
    pages INTEGER NOT NULL DEFAULT 0 CHECK(pages>=0),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(kind,resource_id),
    CHECK ((state='completed')=(completed_at IS NOT NULL))
);
CREATE INDEX account_closure_resources_pending ON billing.account_closure_resources(user_id,kind,resource_id) WHERE state='pending';
-- +goose Down
-- Retain remote cleanup evidence and ownership across application rollback.
SELECT 1;
