-- +goose Up
ALTER TABLE spaces
    ADD COLUMN is_personal BOOLEAN NOT NULL DEFAULT FALSE;

DROP INDEX spaces_one_owned_per_user_idx;

CREATE UNIQUE INDEX spaces_one_personal_per_user_idx
    ON spaces(owner_user_id)
    WHERE is_personal;

CREATE UNIQUE INDEX spaces_one_additional_per_user_idx
    ON spaces(owner_user_id)
    WHERE NOT is_personal;

-- +goose Down
DROP INDEX spaces_one_additional_per_user_idx;
DROP INDEX spaces_one_personal_per_user_idx;

ALTER TABLE spaces
    DROP COLUMN is_personal;

CREATE UNIQUE INDEX spaces_one_owned_per_user_idx ON spaces(owner_user_id);
