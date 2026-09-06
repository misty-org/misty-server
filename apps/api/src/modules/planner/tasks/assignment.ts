import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { taskAudience } from "../access.js";
import { assignmentContext, TaskEffectInvalid, type TaskEffect } from "./effects-model.js";

/** Port of ClaimAssignedAgentTaskRun. All downstream work commits with the
 * source effect; no model/network call occurs while these locks are held. */
export async function createAssignedTaskRun(tx: PoolClient, effect: TaskEffect) {
  if (!["created", "updated"].includes(effect.event_kind)) return;
  const agentId = effect.payload.task.assignee_agent_id;
  if (typeof agentId !== "string" || !agentId) return;
  const task = (await tx.query(`SELECT t.id FROM space_tasks t WHERE t.id=$1 AND t.space_id=$2 AND t.assignee_agent_id=$3
    AND t.archived_at IS NULL AND ${taskAudience("$4")} FOR UPDATE OF t`, [effect.task_id, effect.space_id, agentId, effect.actor_user_id])).rows[0];
  if (!task) return;
  const assignment = await tx.query(`SELECT 1 FROM space_task_activity a WHERE a.task_id=$1 AND a.kind='assigned'
    AND a.actor_agent_id IS NULL AND a.metadata->>'agent_id'=$2 AND a.metadata->>'task_version'=$3
    AND NOT EXISTS(SELECT 1 FROM space_task_activity newer WHERE newer.task_id=a.task_id AND newer.kind='assigned'
      AND newer.actor_agent_id IS NULL AND (newer.metadata->>'task_version')::bigint>$3::bigint)`, [effect.task_id, agentId, effect.task_version]);
  if (!assignment.rowCount) return;
  if ((await tx.query(`SELECT id FROM space_runs WHERE source_task_id=$1 AND agent_id=$2
    AND action_envelope->>'assignment_task_version'=$3`, [effect.task_id, agentId, effect.task_version])).rowCount) return;
  const version = (await tx.query(`SELECT v.id,v.version,v.name,v.instructions,v.model_id,v.reasoning_effort,v.default_run_mode
    FROM personal_agents a JOIN personal_agent_versions v ON v.agent_id=a.id AND v.version=a.version
    WHERE a.id=$1 AND a.owner_user_id=$2 AND a.enabled AND a.deleted_at IS NULL FOR SHARE OF a,v`, [agentId, effect.actor_user_id])).rows[0];
  if (!version) return;
  const { mode, contexts } = assignmentContext(effect.payload.agent_run, version.default_run_mode);
  for (const context of contexts) {
    if (!(await tx.query("SELECT id FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL FOR SHARE",
      [context.device_id, effect.actor_user_id])).rowCount) throw new TaskEffectInvalid("task_device_unavailable");
  }
  const runId = `run_${randomUUID()}`;
  const snapshot = { id: agentId, version: Number(version.version), version_id: version.id, name: version.name,
    instructions: version.instructions, model_id: version.model_id, reasoning_effort: version.reasoning_effort, default_run_mode: version.default_run_mode };
  const envelope = { trigger: "task_assignment", task_id: effect.task_id, assignment_task_version: Number(effect.task_version),
    approved_agent_version_id: version.id, allowed_tools: ["tasks.query", "tasks.update_assigned", "task.activity.write", "attached_files.read"], approval_mode: "explicit_assignment" };
  const inserted = await tx.query(`INSERT INTO space_runs(id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,
    trigger_kind,state,input,result,requesting_member_id,source_type,agent_id,capability_id,outputs,artifacts,attempt,source_task_id,
    action_envelope,owner_user_id,initial_run_mode,effective_run_mode,agent_version_id,agent_version_snapshot,context_bindings,execution_owner)
    VALUES($1,$2,'agent',$3,$4,$4,'task_assignment','queued',$5::jsonb,'{}',$4,'task',$3,'task_assignment','{}','[]',1,$6,
    $7::jsonb,$4,$8,$8,NULL,$9::jsonb,$10::jsonb,'hono') ON CONFLICT DO NOTHING RETURNING id`,
  [runId, effect.space_id, agentId, effect.actor_user_id, JSON.stringify({ task: effect.payload.task, source_refs: effect.payload.task.source_refs }),
    effect.task_id, JSON.stringify(envelope), mode, JSON.stringify(snapshot), JSON.stringify(contexts)]);
  if (!inserted.rowCount) return;
  for (const context of contexts) {
    await tx.query(`INSERT INTO agent_run_contexts(id,run_id,owner_user_id,space_id,device_id,kind,opaque_ref,display_name,capabilities,metadata,expires_at)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,now()+interval '24 hours')`,
    [`context_${randomUUID()}`, runId, effect.actor_user_id, effect.space_id, context.device_id, context.kind,
      context.opaque_ref, context.display_name, JSON.stringify(context.capabilities), JSON.stringify(context.metadata)]);
  }
  await tx.query("INSERT INTO agent_run_jobs(run_id,space_id,task_id,agent_id) VALUES($1,$2,$3,$4)", [runId, effect.space_id, effect.task_id, agentId]);
  await tx.query(`INSERT INTO space_task_activity(id,space_id,task_id,actor_kind,actor_agent_id,run_id,kind,message,metadata)
    VALUES($1,$2,$3,'agent',$4,$5,'progress','Queued to work on this task',$6::jsonb)`,
  [randomUUID(), effect.space_id, effect.task_id, agentId, runId, JSON.stringify({ task_version: Number(effect.task_version) })]);
  const event = (await tx.query<{ id: string }>(`INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
    VALUES($1,'agent.run.queued',$2,$3,$4::jsonb) RETURNING id`, [effect.space_id, effect.actor_user_id, runId,
    JSON.stringify({ agent_id: agentId, source_type: "task", task_id: effect.task_id })])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
}
