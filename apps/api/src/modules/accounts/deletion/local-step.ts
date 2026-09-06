import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { cancelMemberRuns, cancelSpaceRuns } from "../../agents/cancellation.js";
import { revokeSpaceCollaboration } from "../../journal/membership.js";
import { notifySpaceDeletion } from "../../spaces/deletion.js";
import { spaceEvent } from "../../spaces/templates.js";
import { affectedSpaces } from "./access.js";
import { createAccountDeletionJobs, type DeletionJob } from "./jobs.js";

/** Trusted cleanup only. Public Space operations require an active account and
 * must never bypass that guard to call this pending-account transition. */
export function createDeletionLocalRepository(pool: Pool) {
  return { async complete(job: DeletionJob, signal: AbortSignal = new AbortController().signal): Promise<boolean> {
    if (job.step !== "local") return false;
    return withTransaction(pool, async tx => {
      signal.throwIfAborted();
      await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
      // Same global lock order as initiation and other Space/account writers.
      const spaces = (await tx.query<{ id: string; owner_user_id: string; lifecycle_state: string }>(
        `SELECT id,owner_user_id,lifecycle_state FROM spaces WHERE id IN (${affectedSpaces}) ORDER BY id FOR UPDATE`, [job.user_id])).rows;
      if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND license_id=$2 AND lifecycle_state='pending_deletion' FOR UPDATE", [job.user_id, job.license_id])).rowCount) return false;
      if ((await tx.query(`SELECT id FROM spaces WHERE id IN (${affectedSpaces}) AND NOT(id=ANY($2::text[])) LIMIT 1`, [job.user_id, spaces.map(s => s.id)])).rowCount) throw new Error("Account Space inventory changed");
      const request = (await tx.query<{ purge_after: string }>(`SELECT purge_after::text FROM account_deletion_requests
        WHERE id=$1 AND user_id=$2 AND cleanup_owner='native' AND status='processing' FOR UPDATE`, [job.request_id, job.user_id])).rows[0];
      if (!request) return false;
      if (!(await tx.query(`SELECT request_id FROM account_deletion_steps WHERE request_id=$1 AND step='local'
        AND state='processing' AND lease_token=$2 AND lease_expires_at>clock_timestamp() FOR UPDATE`, [job.request_id, job.lease_token])).rowCount) return false;
      const dependencies = await tx.query(`SELECT step FROM account_deletion_steps WHERE request_id=$1
        AND step IN ('payments','providers') AND state='completed' FOR SHARE`, [job.request_id]);
      if (dependencies.rowCount !== 2) return false;
      // Recheck the initiation ownership barrier after provider I/O. Never
      // silently delete a newly shared active Space if a legacy writer raced.
      if ((await tx.query(`SELECT s.id FROM spaces s WHERE s.owner_user_id=$1 AND s.lifecycle_state='active'
        AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=s.id AND m.user_id<>$1) LIMIT 1`, [job.user_id])).rowCount) throw new Error("Account still owns an active shared Space");
      for (const space of spaces) {
        signal.throwIfAborted();
        if (space.owner_user_id === job.user_id) {
          if (space.lifecycle_state === "deleted") continue;
          await cancelSpaceRuns(tx, space.id);
          await revokeSpaceCollaboration(tx, space.id);
          // Existing, later Space retention deadlines are never shortened.
          await tx.query(`UPDATE spaces SET lifecycle_state='pending_deletion',deletion_requested_at=COALESCE(deletion_requested_at,now()),
            permanent_delete_after=GREATEST(permanent_delete_after,$2::timestamptz),updated_at=now() WHERE id=$1`, [space.id, request.purge_after]);
          if (space.lifecycle_state === "active") await spaceEvent(tx, space.id, job.user_id, "space.deletion_requested", space.id, { reason: "account_deleted" });
          await notifySpaceDeletion(tx, space.id);
        } else {
          const member = await tx.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2 AND role='member'", [space.id, job.user_id]);
          if ((await tx.query("SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2", [space.id, job.user_id])).rowCount) throw new Error("Account membership ownership is inconsistent");
          await tx.query(`DELETE FROM space_conversation_members cm USING space_conversations c
            WHERE cm.conversation_id=c.id AND c.space_id=$1 AND cm.user_id=$2`, [space.id, job.user_id]);
          await cancelMemberRuns(tx, space.id, job.user_id);
          await revokeSpaceCollaboration(tx, space.id);
          if (member.rowCount) await spaceEvent(tx, space.id, job.user_id, "member.left", job.user_id, {});
          await tx.query("SELECT pg_notify('misty_space_control',$1)", [JSON.stringify({ type: "member.left", space_id: space.id, user_ids: [job.user_id] })]);
        }
      }
      signal.throwIfAborted();
      // Migration 157 queues both legacy and immutable avatar keys atomically.
      // Completion here means durable cleanup intent, not remote object erasure.
      await tx.query("UPDATE users SET avatar_object_key=NULL,avatar_version=0 WHERE id=$1", [job.user_id]);
      await tx.query("DELETE FROM app_runtime_sessions WHERE user_id=$1", [job.user_id]);
      // Account write lock excludes installation and App purge writers. Preserve
      // an existing uninstall/purge attempt and its original retention deadline.
      await tx.query(`WITH changed AS (
        UPDATE user_app_installations SET state='recoverable',pinned=false,uninstalled_at=now(),
          data_deletion_at=$2,updated_at=now() WHERE user_id=$1 AND state='installed' RETURNING user_id,app_id
      ), queued AS (
        INSERT INTO app_data_deletion_jobs(user_id,app_id,delete_at) SELECT user_id,app_id,$2 FROM changed
        ON CONFLICT(user_id,app_id) DO UPDATE SET state='pending',delete_at=EXCLUDED.delete_at,attempts=0,
          last_error='',started_at=NULL,completed_at=NULL,updated_at=now() RETURNING user_id,app_id
      ) INSERT INTO app_install_events(user_id,app_id,event_type,metadata)
        SELECT user_id,app_id,'uninstalled',jsonb_build_object('reason','account_deleted','data_deletion_at',$2::timestamptz) FROM queued`, [job.user_id, request.purge_after]);
      signal.throwIfAborted();
      const completed = await tx.query(`UPDATE account_deletion_steps SET state='completed',lease_token=NULL,lease_expires_at=NULL,
        completed_at=now(),updated_at=now(),last_error_code='',result='{"outcome":"local_cleanup_scheduled"}'::jsonb
        WHERE request_id=$1 AND step='local' AND state='processing' AND lease_token=$2 AND lease_expires_at>clock_timestamp()`, [job.request_id, job.lease_token]);
      // Failure must roll back every local effect, including durable outboxes.
      if (!completed.rowCount) throw new Error("Account deletion lease expired");
      await tx.query(`UPDATE account_deletion_requests SET status='scheduled',updated_at=now(),
        last_error_code=CASE WHEN last_error_code='local_cleanup_unavailable' THEN '' ELSE last_error_code END WHERE id=$1`, [job.request_id]);
      signal.throwIfAborted(); return true;
    }, { mode: "service" });
  } };
}

/** Remains unmounted until purge, remaining providers and Go handover are ready. */
export function createDeletionLocalWorker(pool: Pool) {
  const jobs = createAccountDeletionJobs(pool), repository = createDeletionLocalRepository(pool);
  return { async runOnce(signal: AbortSignal = new AbortController().signal): Promise<boolean> {
    signal.throwIfAborted(); const job = await jobs.claim("local"); if (!job) return false;
    try { await repository.complete(job, AbortSignal.any([signal, AbortSignal.timeout(25_000)])); }
    catch { await jobs.retry(job); }
    return true;
  } };
}
