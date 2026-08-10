-- +goose Up
UPDATE spaces
SET name = 'Default space', updated_at = NOW()
WHERE is_personal AND name = 'Personal';

-- +goose Down
UPDATE spaces
SET name = 'Personal', updated_at = NOW()
WHERE is_personal AND name = 'Default space';
