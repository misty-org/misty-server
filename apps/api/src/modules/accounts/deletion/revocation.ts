import type { PoolClient } from "pg";
import { revokeSpaceCollaboration } from "../../journal/membership.js";
import { cancelAccountAi } from "../../misty/cancellation.js";

/** Caller holds every affected Space and then the account write lock. Keep
 * encrypted provider credentials for the durable revocation worker, but remove
 * all credentials that could turn into a new account/app/provider authorization.
 */
export async function disableAccount(tx: PoolClient, userId: string, email: string, spaces: string[], signal: AbortSignal) {
  signal.throwIfAborted(); await tx.query("UPDATE users SET lifecycle_state='pending_deletion',deletion_requested_at=now() WHERE id=$1", [userId]);
  await cancelAccountAi(tx, userId, signal);
  for (const table of ["sessions", "app_runtime_sessions", "password_reset_tokens", "auth_handoff_tokens", "connection_authorization_requests", "connected_account_oauth_states", "cloud_oauth_states", "provider_oauth_states", "cloud_credential_handoffs", "github_credential_handoffs", "github_app_setup_states"] as const) {
    signal.throwIfAborted(); await tx.query(`DELETE FROM ${table} WHERE user_id=$1`, [userId]);
  }
  signal.throwIfAborted(); await tx.query(`UPDATE password_recovery_jobs SET state='superseded',lease_owner=NULL,lease_expires_at=NULL,updated_at=now()
    WHERE (email=LOWER($2) OR issued_user_id=$1) AND state IN ('pending','processing')`, [userId, email]);
  signal.throwIfAborted(); await tx.query("UPDATE trusted_devices SET revoked_at=now(),updated_at=now() WHERE user_id=$1 AND revoked_at IS NULL", [userId]);
  signal.throwIfAborted(); await tx.query("UPDATE device_pairs SET state='revoked',revoked_at=now(),updated_at=now() WHERE owner_user_id=$1 AND state='active'", [userId]);
  signal.throwIfAborted(); await tx.query("DELETE FROM device_pairing_sessions WHERE owner_user_id=$1", [userId]);
  signal.throwIfAborted(); await tx.query("DELETE FROM device_presence WHERE owner_user_id=$1", [userId]);
  signal.throwIfAborted(); await tx.query("UPDATE self_host_accounts SET disabled_at=now(),updated_at=now() WHERE user_id=$1 AND disabled_at IS NULL", [userId]);
  signal.throwIfAborted(); await tx.query("UPDATE self_host_enrollment_invitations SET revoked_at=now() WHERE created_by=$1 AND consumed_at IS NULL AND revoked_at IS NULL", [userId]);
  signal.throwIfAborted(); await tx.query("UPDATE personal_agents SET enabled=false,updated_at=now() WHERE owner_user_id=$1 AND deleted_at IS NULL", [userId]);
  const own = "(owner_user_id=$1 OR requesting_member_id=$1 OR billing_user_id=$1 OR initiated_by_user_id=$1 OR agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1))";
  signal.throwIfAborted(); await tx.query(`WITH canceled AS (
    UPDATE space_runs SET state='canceled',runtime_phase='canceled',error_code='account_disabled',
      approval_state=CASE WHEN approval_state='pending' THEN 'denied' ELSE approval_state END,
      device_wait_hook_token='',device_wait_expires_at=NULL,canceled_at=now(),completed_at=now(),updated_at=now()
    WHERE ${own} AND state IN ('queued','running','cooldown','retrying','awaiting_approval','awaiting_device') RETURNING id
  ) UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=now(),updated_at=now()
    WHERE run_id IN (SELECT id FROM canceled) AND state IN ('queued','leased','dispatched')`, [userId]);
  const canceled = `SELECT id FROM space_runs WHERE ${own} AND state='canceled' AND error_code='account_disabled'`;
  signal.throwIfAborted(); await tx.query(`UPDATE agent_run_tool_approvals SET state='denied',decided_at=now() WHERE run_id IN (${canceled}) AND state='pending'`, [userId]);
  signal.throwIfAborted(); await tx.query(`UPDATE agent_run_contexts SET state='detached',updated_at=now() WHERE run_id IN (${canceled}) AND state='attached'`, [userId]);
  signal.throwIfAborted(); await tx.query(`UPDATE workflow_device_node_jobs SET state='canceled',error_code='account_disabled',completed_at=now(),
    leased_device_id=NULL,lease_token_hash=NULL,lease_expires_at=NULL WHERE (user_id=$1 OR run_id IN (${canceled})) AND state IN ('queued','leased')`, [userId]);
  signal.throwIfAborted(); await tx.query("UPDATE space_agents SET schedules_enabled=false WHERE creator_user_id=$1", [userId]);
  signal.throwIfAborted(); await tx.query("UPDATE space_workflows SET schedules_enabled=false WHERE creator_user_id=$1", [userId]);
  for (const spaceId of spaces) {
    signal.throwIfAborted(); await revokeSpaceCollaboration(tx, spaceId);
    signal.throwIfAborted(); await tx.query("SELECT pg_notify('misty_space_control',$1)", [JSON.stringify({ type: "account.disabled", space_id: spaceId, user_ids: [userId] })]);
  }
}
