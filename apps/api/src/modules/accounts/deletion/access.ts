import type { PoolClient } from "pg";
import { AccountUnavailable } from "../repository.js";
import { AccountDeletionAlreadyPending, AccountDeletionUnavailable } from "./model.js";

export const affectedSpaces = `SELECT id FROM spaces WHERE owner_user_id=$1
  UNION SELECT space_id FROM space_members WHERE user_id=$1
  UNION SELECT space_id FROM space_runs WHERE owner_user_id=$1 OR requesting_member_id=$1 OR billing_user_id=$1 OR initiated_by_user_id=$1
    OR agent_id IN (SELECT id FROM personal_agents WHERE owner_user_id=$1)
  UNION SELECT space_id FROM space_agents WHERE creator_user_id=$1
  UNION SELECT space_id FROM space_workflows WHERE creator_user_id=$1
  UNION SELECT space_id FROM space_integrations WHERE connected_by_user_id=$1
  UNION SELECT space_id FROM space_provider_credentials WHERE user_id=$1
  UNION SELECT space_id FROM ai_invocations WHERE user_id=$1
  UNION SELECT space_id FROM ai_invocation_contexts WHERE user_id=$1`;
export async function lockDeletionAccount(tx: PoolClient, userId: string, sessionHash: string) {
  const spaces = (await tx.query<{ id: string }>(`SELECT id FROM spaces WHERE id IN (${affectedSpaces}) ORDER BY id FOR UPDATE`, [userId])).rows.map(row => row.id);
  const user = (await tx.query<{ password_hash: string; email: string; lifecycle_state: string; license_id: string }>(
    "SELECT password_hash,email,lifecycle_state,license_id FROM users WHERE id=$1 FOR UPDATE", [userId])).rows[0];
  if (!user) throw new AccountUnavailable();
  if (user.lifecycle_state !== "active") throw new AccountDeletionAlreadyPending();
  const session = (await tx.query<{ expires_at: string }>("SELECT expires_at::text FROM sessions WHERE user_id=$1 AND token_hash=$2 AND expires_at>clock_timestamp() FOR UPDATE", [userId, sessionHash])).rows[0];
  if (!session) throw new AccountUnavailable();
  // A membership/grant committed while Space locks were being acquired cannot
  // make us acquire a new Space after the account lock. Retry the entire request.
  const changed = await tx.query(`SELECT id FROM spaces WHERE id IN (${affectedSpaces}) AND NOT(id=ANY($2::text[])) LIMIT 1`, [userId, spaces]);
  if (changed.rowCount) throw new AccountDeletionUnavailable();
  return { ...user, spaces, sessionExpiresAt: session.expires_at };
}
