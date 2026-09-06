import type { PoolClient } from "pg";

/** Trusted identity, with Space -> account -> settings/member lock order. */
export async function lockMistyAccount(tx: PoolClient, userId: string, spaceId: string | null, initialize = false) {
  if (spaceId && !(await tx.query("SELECT id FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [spaceId])).rowCount) return false;
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [userId])).rowCount) return false;
  if (initialize) await tx.query("INSERT INTO ai_user_settings(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING", [userId]);
  if (!(await tx.query("SELECT user_id FROM ai_user_settings WHERE user_id=$1 AND enabled FOR SHARE", [userId])).rowCount) return false;
  if (spaceId && !(await tx.query("SELECT user_id FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE", [spaceId,userId])).rowCount) return false;
  return true;
}

export async function lockMistyConversation(tx: PoolClient, userId: string, spaceId: string | null, conversationId: string | null) {
  if (!conversationId) return true;
  return !!(await tx.query(`SELECT id FROM agent_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL
    AND space_id IS NOT DISTINCT FROM $3::text FOR SHARE`, [conversationId,userId,spaceId])).rowCount;
}
