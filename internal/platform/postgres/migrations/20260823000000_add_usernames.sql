-- +goose Up
ALTER TABLE users ADD COLUMN username TEXT;

SET LOCAL app.rls_mode = 'service';

UPDATE users
SET username = CASE LOWER(email)
    WHEN 'mtccool668@gmail.com' THEN 'mtccool668'
    WHEN 'justnatureusa@gmail.com' THEN 'justnatureusa'
    WHEN 'monicatsai2872gmail.com' THEN 'monicatsai2872'
    WHEN 'monicatsai2872@gmail.com' THEN 'monicatsai2872'
    WHEN 'mattdev727@gmail.com' THEN 'mattdev727'
END
WHERE LOWER(email) IN (
    'mtccool668@gmail.com',
    'justnatureusa@gmail.com',
    'monicatsai2872gmail.com',
    'monicatsai2872@gmail.com',
    'mattdev727@gmail.com'
);

-- +goose StatementBegin
DO $$
DECLARE
    current_user_row RECORD;
    base_username TEXT;
    candidate_username TEXT;
    candidate_suffix TEXT;
    candidate_number INTEGER;
BEGIN
    FOR current_user_row IN
        SELECT id, email
        FROM users
        WHERE username IS NULL
        ORDER BY created_at, id
    LOOP
        base_username := LOWER(REGEXP_REPLACE(SPLIT_PART(current_user_row.email, '@', 1), '[^a-z0-9_]+', '_', 'g'));
        base_username := TRIM(BOTH '_' FROM base_username);
        IF CHAR_LENGTH(base_username) < 3 THEN
            base_username := 'user';
        END IF;
        base_username := LEFT(base_username, 30);
        candidate_username := base_username;
        candidate_number := 1;

        WHILE EXISTS (SELECT 1 FROM users WHERE LOWER(username) = candidate_username) LOOP
            candidate_number := candidate_number + 1;
            candidate_suffix := '_' || candidate_number::TEXT;
            candidate_username := LEFT(base_username, 30 - CHAR_LENGTH(candidate_suffix)) || candidate_suffix;
        END LOOP;

        UPDATE users SET username = candidate_username WHERE id = current_user_row.id;
    END LOOP;
END
$$;
-- +goose StatementEnd

ALTER TABLE users ALTER COLUMN username SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_username_format_check
    CHECK (username ~ '^[a-z0-9_]{3,30}$');
CREATE UNIQUE INDEX users_username_unique_idx ON users (LOWER(username));

-- +goose Down
DROP INDEX IF EXISTS users_username_unique_idx;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_format_check;
ALTER TABLE users DROP COLUMN IF EXISTS username;
