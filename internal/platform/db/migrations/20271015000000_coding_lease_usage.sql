-- +goose Up
-- What each lease's tool reported, by token class, so billing is settled
-- once when the lease ends rather than on every usage report.
ALTER TABLE coding_leases
    ADD COLUMN input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    ADD COLUMN output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    ADD COLUMN cache_write_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    ADD COLUMN cache_read_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    ADD COLUMN settled_at timestamptz;
CREATE INDEX coding_leases_unsettled ON coding_leases(invocation_id) WHERE settled_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS coding_leases_unsettled;
ALTER TABLE coding_leases
    DROP COLUMN IF EXISTS input_tokens,
    DROP COLUMN IF EXISTS output_tokens,
    DROP COLUMN IF EXISTS cache_write_tokens,
    DROP COLUMN IF EXISTS cache_read_tokens,
    DROP COLUMN IF EXISTS settled_at;
