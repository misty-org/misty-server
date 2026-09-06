-- +goose Up
-- RLS helpers call other public helpers by unqualified name. Resolve those from
-- the trusted application schema after pg_catalog, never a caller's search path.
ALTER FUNCTION misty_protect_default_space() SET search_path=pg_catalog,public,pg_temp;
-- +goose Down
SELECT 1;
