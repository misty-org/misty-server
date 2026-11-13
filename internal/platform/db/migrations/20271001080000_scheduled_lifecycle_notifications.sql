-- +goose Up
-- Committed hints for scheduled runs and lifecycle deadlines. Time passing is
-- covered by each queue's database-clock deadline; hints cover new or earlier work.
CREATE TRIGGER ai_recap_schedule_insert_notify AFTER INSERT ON ai_recaps
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('scheduled');
CREATE TRIGGER ai_recap_schedule_update_notify AFTER UPDATE OF enabled,next_run_at,state,lease_until ON ai_recaps
FOR EACH ROW WHEN ((OLD.enabled,OLD.next_run_at,OLD.state,OLD.lease_until) IS DISTINCT FROM (NEW.enabled,NEW.next_run_at,NEW.state,NEW.lease_until))
EXECUTE FUNCTION misty_notify_worker_queue('scheduled');
CREATE TRIGGER scheduled_task_insert_notify AFTER INSERT ON scheduled_tasks
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('scheduled');
CREATE TRIGGER scheduled_task_update_notify AFTER UPDATE OF enabled,next_run_at,state,lease_until ON scheduled_tasks
FOR EACH ROW WHEN ((OLD.enabled,OLD.next_run_at,OLD.state,OLD.lease_until) IS DISTINCT FROM (NEW.enabled,NEW.next_run_at,NEW.state,NEW.lease_until))
EXECUTE FUNCTION misty_notify_worker_queue('scheduled');
-- Re-enabling AI makes that user's existing schedules eligible again.
CREATE TRIGGER ai_user_settings_schedule_notify AFTER UPDATE OF enabled ON ai_user_settings
FOR EACH ROW WHEN (OLD.enabled IS DISTINCT FROM NEW.enabled)
EXECUTE FUNCTION misty_notify_worker_queue('scheduled');

CREATE TRIGGER ai_cleanup_job_insert_notify AFTER INSERT ON ai_cleanup_jobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('ai-cleanup');
CREATE TRIGGER ai_cleanup_job_update_notify AFTER UPDATE OF state,available_at ON ai_cleanup_jobs
FOR EACH ROW WHEN ((OLD.state,OLD.available_at) IS DISTINCT FROM (NEW.state,NEW.available_at))
EXECUTE FUNCTION misty_notify_worker_queue('ai-cleanup');

CREATE TRIGGER account_deletion_insert_notify AFTER INSERT ON account_deletion_requests
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('account-deletion');
CREATE TRIGGER account_deletion_update_notify AFTER UPDATE OF status,purge_after,cleanup_owner ON account_deletion_requests
FOR EACH ROW WHEN ((OLD.status,OLD.purge_after,OLD.cleanup_owner) IS DISTINCT FROM (NEW.status,NEW.purge_after,NEW.cleanup_owner))
EXECUTE FUNCTION misty_notify_worker_queue('account-deletion');

CREATE TRIGGER rendition_reservation_expiry_insert_notify AFTER INSERT ON space_rendition_reservations
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('rendition-reservations');
CREATE TRIGGER rendition_reservation_expiry_update_notify AFTER UPDATE OF state,expires_at ON space_rendition_reservations
FOR EACH ROW WHEN ((OLD.state,OLD.expires_at) IS DISTINCT FROM (NEW.state,NEW.expires_at))
EXECUTE FUNCTION misty_notify_worker_queue('rendition-reservations');
CREATE INDEX space_rendition_reservations_active_expiry_idx ON space_rendition_reservations(expires_at) WHERE state='active';
-- Bounded seven-day event retention batches.
CREATE INDEX space_events_created_idx ON space_events(created_at);

-- +goose Down
DROP INDEX IF EXISTS space_events_created_idx;
DROP INDEX IF EXISTS space_rendition_reservations_active_expiry_idx;
DROP TRIGGER IF EXISTS rendition_reservation_expiry_update_notify ON space_rendition_reservations;
DROP TRIGGER IF EXISTS rendition_reservation_expiry_insert_notify ON space_rendition_reservations;
DROP TRIGGER IF EXISTS account_deletion_update_notify ON account_deletion_requests;
DROP TRIGGER IF EXISTS account_deletion_insert_notify ON account_deletion_requests;
DROP TRIGGER IF EXISTS ai_cleanup_job_update_notify ON ai_cleanup_jobs;
DROP TRIGGER IF EXISTS ai_cleanup_job_insert_notify ON ai_cleanup_jobs;
DROP TRIGGER IF EXISTS ai_user_settings_schedule_notify ON ai_user_settings;
DROP TRIGGER IF EXISTS scheduled_task_update_notify ON scheduled_tasks;
DROP TRIGGER IF EXISTS scheduled_task_insert_notify ON scheduled_tasks;
DROP TRIGGER IF EXISTS ai_recap_schedule_update_notify ON ai_recaps;
DROP TRIGGER IF EXISTS ai_recap_schedule_insert_notify ON ai_recaps;
