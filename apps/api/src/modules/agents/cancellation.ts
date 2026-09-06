import type { PoolClient } from "pg";

/** Caller holds the Space write lock and has authorized the membership change. */
export async function cancelMemberRuns(tx: PoolClient, spaceId: string, userId: string) {
  return cancelRuns(tx, spaceId, userId, "space_membership_revoked");
}
export async function cancelSpaceRuns(tx: PoolClient, spaceId: string) {
  return cancelRuns(tx, spaceId, null, "space_deleted");
}
async function cancelRuns(tx: PoolClient, spaceId: string, userId: string | null, code: string) {
  const values = [spaceId, userId, code];
  await tx.query(`WITH canceled AS (
    UPDATE space_runs SET state='canceled',runtime_phase='canceled',error_code=$3,
      approval_state=CASE WHEN approval_state='pending' THEN 'denied' ELSE approval_state END,
      device_wait_hook_token='',device_wait_expires_at=NULL,canceled_at=now(),completed_at=now(),updated_at=now()
    WHERE space_id=$1 AND ($2::text IS NULL OR owner_user_id=$2)
      AND state IN ('queued','running','cooldown','retrying','awaiting_approval','awaiting_device') RETURNING id
  ) UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=now(),updated_at=now()
    WHERE run_id IN (SELECT id FROM canceled) AND state IN ('queued','leased','dispatched')`, values);
  await tx.query(`UPDATE agent_run_tool_approvals SET state='denied',decided_at=now()
    WHERE run_id IN (SELECT id FROM space_runs WHERE space_id=$1 AND ($2::text IS NULL OR owner_user_id=$2) AND state='canceled' AND error_code=$3)
      AND state='pending'`, values);
  await tx.query(`UPDATE agent_run_contexts SET state='detached',updated_at=now()
    WHERE run_id IN (SELECT id FROM space_runs WHERE space_id=$1 AND ($2::text IS NULL OR owner_user_id=$2) AND state='canceled' AND error_code=$3)
      AND state='attached'`, values);
  await tx.query(`UPDATE workflow_device_node_jobs SET state='canceled',error_code=$3,completed_at=now(),
      leased_device_id=NULL,lease_token_hash=NULL,lease_expires_at=NULL
    WHERE run_id IN (SELECT id FROM space_runs WHERE space_id=$1 AND ($2::text IS NULL OR owner_user_id=$2) AND state='canceled' AND error_code=$3)
      AND state IN ('queued','leased')`, values);
  await tx.query("UPDATE space_agents SET schedules_enabled=false WHERE space_id=$1 AND ($2::text IS NULL OR creator_user_id=$2)", values.slice(0, 2));
  await tx.query("UPDATE space_workflows SET schedules_enabled=false WHERE space_id=$1 AND ($2::text IS NULL OR creator_user_id=$2)", values.slice(0, 2));
}
