-- +goose Up
-- Reserved successor to the retired per-invoker permission migration. The
-- breaking creator-scoped migration owns the actual data removal and defaults.
SELECT 1;

-- +goose Down
SELECT 1;
