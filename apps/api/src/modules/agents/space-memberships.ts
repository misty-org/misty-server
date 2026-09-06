import type { PoolClient } from "pg";
import { clientInteger } from "../spaces/model.js";
/** Caller must authorize and lock the actor's active Space membership first. */
export async function readSpaceAgents(tx: PoolClient, userId: string, spaceId: string) {
  const rows = (await tx.query(`SELECT 'companion:'||$1||':'||a.id AS id,$1::text AS space_id,a.id AS agent_id,a.owner_user_id,
    (a.owner_user_id=$2 AND a.enabled) AS can_control,v.name,v.description,v.icon,v.avatar,
    CASE WHEN a.owner_user_id=$2 THEN NULLIF(v.instructions,'') END AS instructions,
    CASE WHEN a.owner_user_id=$2 THEN NULLIF(v.model_id,'') END AS model_id,
    CASE WHEN a.owner_user_id=$2 THEN NULLIF(v.reasoning_effort,'') END AS reasoning_effort,
    v.default_run_mode,a.enabled,v.version,a.created_at,a.updated_at,
    CASE WHEN NOT a.enabled THEN 'disabled' WHEN summary.awaiting_count>0 THEN 'awaiting_approval'
      WHEN summary.device_count>0 THEN 'awaiting_device' WHEN summary.working_count>0 THEN 'working'
      WHEN latest.state IN ('failed','completed_with_errors') THEN 'failed' ELSE 'ready' END AS work_state,
    summary.awaiting_count+CASE WHEN latest.state IN ('failed','completed_with_errors') THEN 1 ELSE 0 END AS attention_count,
    summary.last_activity_at,task.source_task_id AS current_task_id
    FROM personal_agents a JOIN personal_agent_versions v ON v.agent_id=a.id AND v.version=a.version
    LEFT JOIN LATERAL (SELECT count(*) FILTER(WHERE r.state='awaiting_approval') AS awaiting_count,
      count(*) FILTER(WHERE r.state='awaiting_device') AS device_count,count(*) FILTER(WHERE r.state IN ('queued','running','cooldown')) AS working_count,
      max(r.updated_at) AS last_activity_at FROM space_runs r WHERE r.space_id=$1 AND r.agent_id=a.id) summary ON true
    LEFT JOIN LATERAL (SELECT r.state FROM space_runs r WHERE r.space_id=$1 AND r.agent_id=a.id ORDER BY r.updated_at DESC,r.id DESC LIMIT 1) latest ON true
    LEFT JOIN LATERAL (SELECT r.source_task_id FROM space_runs r WHERE r.space_id=$1 AND r.agent_id=a.id AND r.source_task_id IS NOT NULL
      AND r.state IN ('queued','running','cooldown','awaiting_approval','failed','completed_with_errors') ORDER BY r.updated_at DESC,r.id DESC LIMIT 1) task ON true
    WHERE a.deleted_at IS NULL AND ((a.owner_user_id=$2 AND a.enabled) OR EXISTS(SELECT 1 FROM space_tasks t WHERE t.space_id=$1 AND t.assignee_agent_id=a.id)
      OR EXISTS(SELECT 1 FROM space_messages m WHERE m.space_id=$1 AND m.sender_agent_id=a.id)) ORDER BY lower(v.name),a.id`, [spaceId, userId])).rows;
  return rows.map((row) => {
    const { instructions, model_id, reasoning_effort, last_activity_at, current_task_id, ...rest } = row;
    return { ...rest, version: clientInteger(row.version), attention_count: clientInteger(row.attention_count),
      ...(instructions ? { instructions } : {}), ...(model_id ? { model_id } : {}), ...(reasoning_effort ? { reasoning_effort } : {}),
      ...(last_activity_at ? { last_activity_at } : {}), ...(current_task_id ? { current_task_id } : {}) };
  });
}
