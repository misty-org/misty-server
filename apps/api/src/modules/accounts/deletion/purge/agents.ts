import type { PoolClient } from "pg";

const ownedAgents = "SELECT id FROM personal_agents WHERE owner_user_id=$1";
export const privateRuns = `(owner_user_id=$1 OR requesting_member_id=$1 OR billing_user_id=$1 OR initiated_by_user_id=$1)`;
export const agentPurgeSpaces = `SELECT space_id AS id FROM space_runs WHERE ${privateRuns} OR agent_id IN (${ownedAgents})
  OR source_agent_conversation_id IN (SELECT id FROM agent_conversations WHERE user_id=$1)
  UNION SELECT space_id FROM space_agent_instances WHERE user_id=$1
  UNION SELECT space_id FROM space_agent_conversations WHERE owner_user_id=$1
  UNION SELECT space_id FROM agent_conversations WHERE user_id=$1
  UNION SELECT space_id FROM agent_run_contexts WHERE owner_user_id=$1
  UNION SELECT space_id FROM misty_memories WHERE user_id=$1`;

/** The caller holds Space/account/request/retention/lease fences. This removes
 * private data only; shared Space content and financial identities stay intact. */
export async function purgePrivateAgentData(tx: PoolClient, userId: string, signal: AbortSignal) {
  const query = async (sql: string) => { signal.throwIfAborted(); return tx.query(sql, [userId]); };
  // Cascades cannot be used to erase another account's inconsistent child rows.
  if ((await query(`SELECT 1 FROM ai_conversation_attachments a JOIN agent_conversations c ON c.id=a.conversation_id
    WHERE c.user_id=$1 AND a.user_id<>$1
    UNION ALL SELECT 1 FROM ai_artifacts a JOIN ai_invocations i ON i.id=a.invocation_id WHERE i.user_id=$1 AND a.user_id<>$1
    UNION ALL SELECT 1 FROM ai_feedback f JOIN ai_invocations i ON i.id=f.invocation_id WHERE i.user_id=$1 AND f.user_id<>$1 LIMIT 1`)).rowCount) throw new Error("Private AI ownership mismatch");
  // Upload intents survive row deletion via migration 163. Remote object erasure
  // remains pending and is not asserted by this SQL phase's receipt.
  await query("DELETE FROM ai_conversation_attachments WHERE user_id=$1");
  await query("DELETE FROM agent_conversation_events WHERE user_id=$1");
  await query("DELETE FROM agent_conversations WHERE user_id=$1");
  await query("DELETE FROM space_agent_conversation_events WHERE user_id=$1");
  await query("DELETE FROM space_agent_conversations WHERE owner_user_id=$1");
  await query("DELETE FROM misty_memories WHERE user_id=$1");
  await query("DELETE FROM ai_feedback WHERE user_id=$1");
  await query("DELETE FROM ai_artifacts WHERE user_id=$1");
  await query("DELETE FROM ai_invocations WHERE user_id=$1");
  await query("DELETE FROM ai_retrieval_documents WHERE owner_user_id=$1 AND privacy_class='private'");
  await query("DELETE FROM ai_surface_preferences WHERE user_id=$1");
  await query("DELETE FROM ai_cleanup_jobs WHERE user_id=$1");
  await query("DELETE FROM ai_user_settings WHERE user_id=$1");
  await query("DELETE FROM space_agent_instances WHERE user_id=$1");
  await query("DELETE FROM agent_run_contexts WHERE owner_user_id=$1");
  await query(`UPDATE agent_run_tool_approvals SET signed_call='',hook_token='',summary='',
    state=CASE WHEN state='pending' THEN 'denied' ELSE state END,decided_at=COALESCE(decided_at,now()) WHERE owner_user_id=$1`);
  await query(`UPDATE personal_agent_mcp_tools SET enabled=false,updated_at=now() WHERE owner_user_id=$1 OR agent_id IN (${ownedAgents})`);
  await query(`UPDATE personal_agent_versions SET name='Deleted Agent',role='',description='',icon='',instructions='',voice_id='',
    avatar='{"kind":"preset","preset_id":"bot","accent":"neutral"}'::jsonb,model_mode='pinned',model_id='deleted',reasoning_effort='',default_run_mode='auto',
    checksum_sha256=encode(sha256(convert_to(id||':'||agent_id||':deleted','UTF8')),'hex') WHERE agent_id IN (${ownedAgents})`);
  await query(`UPDATE personal_agents SET name='Deleted Agent',role='',description='',icon='',instructions='',voice_id='',
    avatar='{"kind":"preset","preset_id":"bot","accent":"neutral"}'::jsonb,model_mode='pinned',model_id='deleted',reasoning_effort='',default_run_mode='auto',
    enabled=false,deleted_at=COALESCE(deleted_at,now()),updated_at=now() WHERE owner_user_id=$1`);
  // Redact owner-authored definitions even in a shared run requested by someone
  // else, while retaining that other requester's result and activity.
  await query(`UPDATE space_runs SET agent_version_snapshot='{}'::jsonb,updated_at=now() WHERE agent_id IN (${ownedAgents})`);
  await query(`UPDATE space_runs SET input='{}'::jsonb,result='{"redacted":true}'::jsonb,outputs='{}'::jsonb,artifacts='[]'::jsonb,
    action_envelope='{}'::jsonb,context_bindings='[]'::jsonb,agent_version_snapshot='{}'::jsonb,error_message=NULL,
    device_wait_hook_token='',device_wait_expires_at=NULL,updated_at=now() WHERE ${privateRuns}`);
  await query(`UPDATE agent_run_jobs SET last_error_message='' WHERE run_id IN (SELECT id FROM space_runs WHERE ${privateRuns})`);
  // Keep idempotency tombstones: deleting them could replay a historical action.
  await query(`UPDATE agent_toolbox_action_journal SET request='{}'::jsonb,result='{"redacted":true}'::jsonb,session_id=NULL,updated_at=now() WHERE user_id=$1`);
}
