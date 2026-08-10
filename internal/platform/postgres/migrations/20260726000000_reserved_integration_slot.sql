-- +goose Up
-- This version is intentionally reserved so databases that applied an earlier
-- migration at the same version remain compatible with the current history.
SELECT 1;

-- +goose Down
SELECT 1;
