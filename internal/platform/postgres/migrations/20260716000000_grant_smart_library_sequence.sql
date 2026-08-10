-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT USAGE, SELECT ON SEQUENCE smart_library_result_sequence TO misty_app;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT USAGE, SELECT ON SEQUENCES TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            REVOKE USAGE, SELECT ON SEQUENCES FROM misty_app;
        REVOKE USAGE, SELECT ON SEQUENCE smart_library_result_sequence FROM misty_app;
    END IF;
END
$$;
-- +goose StatementEnd
