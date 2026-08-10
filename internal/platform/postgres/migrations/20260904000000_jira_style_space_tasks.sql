-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_task_counters (
    space_id TEXT PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
    last_number BIGINT NOT NULL DEFAULT 0 CHECK (last_number >= 0)
);

ALTER TABLE space_tasks
    ADD COLUMN task_number BIGINT,
    ADD COLUMN task_key TEXT,
    ADD COLUMN priority TEXT NOT NULL DEFAULT 'medium',
    ADD COLUMN rank BIGINT;

WITH numbered AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at,id) AS task_number,
           ROW_NUMBER() OVER (PARTITION BY space_id,status ORDER BY due_at NULLS LAST,updated_at,id) * 1024 AS rank
    FROM space_tasks
)
UPDATE space_tasks AS task
SET task_number=numbered.task_number,
    task_key='MST-' || numbered.task_number::TEXT,
    rank=numbered.rank
FROM numbered
WHERE numbered.id=task.id;

INSERT INTO space_task_counters(space_id,last_number)
SELECT space_id,COALESCE(MAX(task_number),0)
FROM space_tasks
GROUP BY space_id;

ALTER TABLE space_tasks
    ALTER COLUMN task_number SET NOT NULL,
    ALTER COLUMN task_key SET NOT NULL,
    ALTER COLUMN rank SET NOT NULL,
    ADD CONSTRAINT space_tasks_priority_check CHECK (priority IN ('high','medium','low')),
    ADD CONSTRAINT space_tasks_rank_check CHECK (rank > 0),
    ADD CONSTRAINT space_tasks_number_unique UNIQUE(space_id,task_number),
    ADD CONSTRAINT space_tasks_key_unique UNIQUE(space_id,task_key);

CREATE INDEX space_tasks_board_idx ON space_tasks(space_id,status,rank,id) WHERE archived_at IS NULL;
CREATE INDEX space_tasks_priority_idx ON space_tasks(space_id,priority,status,rank) WHERE archived_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_tasks_priority_idx;
DROP INDEX IF EXISTS space_tasks_board_idx;
ALTER TABLE space_tasks
    DROP CONSTRAINT IF EXISTS space_tasks_key_unique,
    DROP CONSTRAINT IF EXISTS space_tasks_number_unique,
    DROP CONSTRAINT IF EXISTS space_tasks_rank_check,
    DROP CONSTRAINT IF EXISTS space_tasks_priority_check,
    DROP COLUMN IF EXISTS rank,
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS task_key,
    DROP COLUMN IF EXISTS task_number;
DROP TABLE IF EXISTS space_task_counters;
-- +goose StatementEnd
