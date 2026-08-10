-- +goose Up
-- +goose StatementBegin
-- Two changes to space_runs.source_type, both in one constraint rewrite.
--
-- 1. 'mika' becomes 'agent_console' as part of removing Mika as a concept.
--    'mika' stays permitted for one release: this migration and the binary
--    rollout are not atomic, so an old binary writing 'mika' after the
--    constraint tightens would fail its insert. A follow-up migration drops it
--    once every deployment is on the new binary.
--
-- 2. Adds 'connector' and 'task', which validRunSource in db/agent_runs.go has
--    always accepted but the original CHECK never listed. Creating a run with
--    either value passes Go validation and then fails at insert time, so this
--    is a live bug rather than a hypothetical one.
--
-- Order matters: the constraint must be dropped before the backfill, or the
-- UPDATE is rejected by the constraint still being enforced.
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_source_type_check;

UPDATE space_runs SET source_type = 'agent_console' WHERE source_type = 'mika';

-- trigger_kind carries the same legacy value but has no CHECK constraint of
-- its own (see 20260802000000_create_spaces.sql), so it is a plain backfill.
UPDATE space_runs SET trigger_kind = 'agent_console' WHERE trigger_kind = 'mika';

ALTER TABLE space_runs ADD CONSTRAINT space_runs_source_type_check
    CHECK (source_type IN (
        'direct',
        'group_mention',
        'agent_console',
        'studio_test',
        'schedule',
        'connector',
        'task',
        'mika' -- transitional; drop once all deployments write 'agent_console'
    ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_source_type_check;

UPDATE space_runs SET source_type = 'mika' WHERE source_type = 'agent_console';
UPDATE space_runs SET trigger_kind = 'mika' WHERE trigger_kind = 'agent_console';

-- Rows added under the widened constraint may hold values the original CHECK
-- rejected, so fold them into the closest legacy value before restoring it.
UPDATE space_runs SET source_type = 'direct' WHERE source_type IN ('connector', 'task');

ALTER TABLE space_runs ADD CONSTRAINT space_runs_source_type_check
    CHECK (source_type IN ('direct', 'group_mention', 'mika', 'studio_test', 'schedule'));
-- +goose StatementEnd
