-- +goose Up
-- Scheduled tasks that drive the browser (roadmap C1). A schedule can name one
-- of the person's computers. When it's due, the run waits for Misty to be open
-- on that computer, which starts it with its own signed browser grant. A run
-- that comes due while the computer is away waits for it and says so; it never
-- runs in the cloud instead.
ALTER TABLE workflow_schedules
    ADD COLUMN device_id text REFERENCES trusted_devices(id) ON DELETE SET NULL,
    -- The occurrence waiting for the computer, and how many later ones passed
    -- while it waited (they fold into this one run).
    ADD COLUMN due_at timestamptz,
    ADD COLUMN missed_count integer NOT NULL DEFAULT 0 CHECK (missed_count >= 0),
    -- The computer that picked the run up proves it with this one-time claim.
    ADD COLUMN device_claim_hash text,
    ADD COLUMN device_claim_until timestamptz;

-- The constraint name comes from the original scheduled_tasks table.
-- +goose StatementBegin
DO $$
DECLARE name text;
BEGIN
    SELECT conname INTO name FROM pg_constraint
    WHERE conrelid = 'workflow_schedules'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%state%idle%running%failed%';
    IF name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE workflow_schedules DROP CONSTRAINT %I', name);
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE workflow_schedules ADD CONSTRAINT workflow_schedules_state_check
    CHECK (state IN ('idle','running','failed','awaiting_device','device_claimed'));
CREATE INDEX workflow_schedules_awaiting_device ON workflow_schedules(device_id) WHERE state IN ('awaiting_device','device_claimed');

-- +goose Down
DROP INDEX IF EXISTS workflow_schedules_awaiting_device;
UPDATE workflow_schedules SET state='idle' WHERE state IN ('awaiting_device','device_claimed');
ALTER TABLE workflow_schedules DROP CONSTRAINT IF EXISTS workflow_schedules_state_check;
ALTER TABLE workflow_schedules ADD CONSTRAINT workflow_schedules_state_check CHECK (state IN ('idle','running','failed'));
ALTER TABLE workflow_schedules
    DROP COLUMN IF EXISTS device_claim_until,
    DROP COLUMN IF EXISTS device_claim_hash,
    DROP COLUMN IF EXISTS missed_count,
    DROP COLUMN IF EXISTS due_at,
    DROP COLUMN IF EXISTS device_id;
