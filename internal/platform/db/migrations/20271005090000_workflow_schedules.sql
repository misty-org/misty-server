-- +goose Up
-- A schedule now belongs to exactly one workflow and always runs that workflow's
-- latest version. Its timing is a set of rules (see workflow_schedule.go) instead
-- of one cadence, so one schedule can say "weekdays at 9 and 17, plus the first
-- Monday of each month at 8". Plain-prompt scheduled tasks are retired.
DELETE FROM scheduled_tasks WHERE method_version_id IS NULL;
ALTER TABLE scheduled_tasks ADD COLUMN method_id text;
UPDATE scheduled_tasks t SET method_id = v.method_id
FROM agent_method_versions v WHERE v.id = t.method_version_id AND v.user_id = t.user_id;
DELETE FROM scheduled_tasks WHERE method_id IS NULL;
-- One schedule per workflow: keep the most recently changed one.
DELETE FROM scheduled_tasks t USING scheduled_tasks newer
WHERE newer.method_id = t.method_id AND (newer.updated_at, newer.id) > (t.updated_at, t.id);

ALTER TABLE scheduled_tasks
  ADD COLUMN rules jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(rules) = 'array'),
  ADD COLUMN skip_dates jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(skip_dates) = 'array');
UPDATE scheduled_tasks SET rules = jsonb_build_array(
  CASE cadence
    WHEN 'once' THEN jsonb_build_object('frequency', 'once',
      'dates', jsonb_build_array(to_char(run_on, 'YYYY-MM-DD')), 'times', jsonb_build_array(local_time))
    ELSE jsonb_build_object(
      'frequency', CASE cadence WHEN 'daily' THEN 'daily' WHEN 'monthly' THEN 'monthly' ELSE 'weekly' END,
      'interval', 1,
      'times', jsonb_build_array(local_time),
      'start_date', to_char(created_at AT TIME ZONE timezone, 'YYYY-MM-DD'),
      'weekdays', CASE cadence WHEN 'weekdays' THEN '[1,2,3,4,5]'::jsonb
        WHEN 'weekly' THEN jsonb_build_array(weekday) ELSE '[]'::jsonb END,
      'month_days', CASE cadence WHEN 'monthly' THEN jsonb_build_array(month_day) ELSE '[]'::jsonb END)
  END);

ALTER TABLE scheduled_tasks DROP CONSTRAINT scheduled_method_owner;
ALTER TABLE scheduled_tasks
  DROP COLUMN title, DROP COLUMN prompt, DROP COLUMN cadence, DROP COLUMN local_time,
  DROP COLUMN weekday, DROP COLUMN month_day, DROP COLUMN run_on, DROP COLUMN agent_id,
  DROP COLUMN method_version_id;
ALTER TABLE scheduled_tasks RENAME COLUMN method_inputs TO inputs;
ALTER TABLE scheduled_tasks
  ALTER COLUMN method_id SET NOT NULL,
  ADD CONSTRAINT workflow_schedules_method FOREIGN KEY (method_id, user_id)
    REFERENCES agent_methods(id, user_id) ON DELETE CASCADE,
  ADD CONSTRAINT workflow_schedules_one_per_workflow UNIQUE (method_id);
ALTER TABLE scheduled_tasks RENAME TO workflow_schedules;
ALTER INDEX scheduled_tasks_due_idx RENAME TO workflow_schedules_due_idx;
ALTER INDEX scheduled_tasks_owner_idx RENAME TO workflow_schedules_owner_idx;
ALTER POLICY scheduled_tasks_owner_policy ON workflow_schedules RENAME TO workflow_schedules_owner_policy;
ALTER TRIGGER scheduled_task_insert_notify ON workflow_schedules RENAME TO workflow_schedule_insert_notify;
ALTER TRIGGER scheduled_task_update_notify ON workflow_schedules RENAME TO workflow_schedule_update_notify;
-- Clients follow their workflows, schedules included, on one account topic.
DROP TRIGGER scheduled_task_account_notify ON workflow_schedules;
DROP TRIGGER scheduled_task_account_update_notify ON workflow_schedules;
CREATE TRIGGER workflow_schedule_account_notify AFTER INSERT OR DELETE ON workflow_schedules
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('workflows', 'user_id');
CREATE TRIGGER workflow_schedule_account_update_notify AFTER UPDATE ON workflow_schedules
FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*)
EXECUTE FUNCTION misty_notify_account_change('workflows', 'user_id');

-- +goose Down
-- Schedules cannot be turned back into cadence tasks; they are removed.
DELETE FROM workflow_schedules;
DROP TRIGGER workflow_schedule_account_update_notify ON workflow_schedules;
DROP TRIGGER workflow_schedule_account_notify ON workflow_schedules;
CREATE TRIGGER scheduled_task_account_notify AFTER INSERT OR DELETE ON workflow_schedules
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('scheduled-tasks', 'user_id');
CREATE TRIGGER scheduled_task_account_update_notify AFTER UPDATE ON workflow_schedules
FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*)
EXECUTE FUNCTION misty_notify_account_change('scheduled-tasks', 'user_id');
ALTER TRIGGER workflow_schedule_update_notify ON workflow_schedules RENAME TO scheduled_task_update_notify;
ALTER TRIGGER workflow_schedule_insert_notify ON workflow_schedules RENAME TO scheduled_task_insert_notify;
ALTER POLICY workflow_schedules_owner_policy ON workflow_schedules RENAME TO scheduled_tasks_owner_policy;
ALTER INDEX workflow_schedules_owner_idx RENAME TO scheduled_tasks_owner_idx;
ALTER INDEX workflow_schedules_due_idx RENAME TO scheduled_tasks_due_idx;
ALTER TABLE workflow_schedules RENAME TO scheduled_tasks;
ALTER TABLE scheduled_tasks DROP CONSTRAINT workflow_schedules_one_per_workflow,
  DROP CONSTRAINT workflow_schedules_method, DROP COLUMN method_id,
  DROP COLUMN rules, DROP COLUMN skip_dates;
ALTER TABLE scheduled_tasks RENAME COLUMN inputs TO method_inputs;
ALTER TABLE scheduled_tasks
  ADD COLUMN title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
  ADD COLUMN prompt text NOT NULL CHECK (char_length(prompt) BETWEEN 1 AND 8000),
  ADD COLUMN cadence text NOT NULL CHECK (cadence IN ('once','daily','weekdays','weekly','monthly')),
  ADD COLUMN local_time text NOT NULL CHECK (local_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  ADD COLUMN weekday smallint NOT NULL DEFAULT 1 CHECK (weekday BETWEEN 0 AND 6),
  ADD COLUMN month_day smallint NOT NULL DEFAULT 1 CHECK (month_day BETWEEN 1 AND 31),
  ADD COLUMN run_on date,
  ADD COLUMN agent_id text REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
  ADD COLUMN method_version_id text,
  ADD CONSTRAINT scheduled_method_owner FOREIGN KEY (method_version_id, user_id) REFERENCES agent_method_versions(id, user_id),
  ADD CHECK (cadence <> 'once' OR run_on IS NOT NULL);
