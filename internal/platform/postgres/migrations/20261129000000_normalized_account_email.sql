-- +goose Up
-- +goose StatementBegin
-- Login and recovery use LOWER(email). Enforce that same identity boundary and
-- index those lookups. Existing case-only collisions deliberately stop migration;
-- resolving account ownership requires review, never automatic merging.
CREATE UNIQUE INDEX users_email_normalized_unique_idx ON users(LOWER(email));
-- +goose StatementEnd

-- +goose Down
DROP INDEX users_email_normalized_unique_idx;
