-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Host-embedded apps authenticate as the signed-in account. Retire tokens from
-- the old packaged runtime so parallel requests cannot carry stale Space grants.
DELETE FROM app_runtime_sessions
WHERE app_id IN ('chat','journal','planner','library','inbox','agents','files','browser','code','terminal');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Short-lived credentials cannot be reconstructed safely.
SELECT 1;
-- +goose StatementEnd
