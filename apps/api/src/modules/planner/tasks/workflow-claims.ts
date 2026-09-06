import { createHash } from "node:crypto";
import type { PoolClient } from "pg";
import { spacePermissions } from "../../spaces/permissions.js";
import { taskAudience } from "../access.js";
import type { TaskEffect } from "./effects-model.js";

export async function claimTaskWorkflows(tx: PoolClient, effect: TaskEffect) {
  const payload = { task: effect.payload.task, eventKind: effect.event_kind };
  const fingerprint = createHash("sha256").update(JSON.stringify(payload)).digest("hex");
  const eventId = `${effect.event_kind}:${effect.task_id}:v${effect.task_version}`;
  const targets = (await tx.query(`SELECT i.id,i.user_id,i.agent_id,w.workflow_version_id,w.trigger_config->>'capabilityId' AS capability_id
    FROM space_agent_instance_workflows w JOIN space_agent_instances i ON i.id=w.instance_id
    WHERE i.space_id=$1 AND w.enabled AND w.consent->>'granted'='true' AND w.trigger_config->>'kind'='task_change'
    AND (NOT $2 OR w.trigger_config->>'includeAgentChanges'='true')
    AND jsonb_typeof(w.trigger_config->'capabilityId')='string' AND w.trigger_config->>'capabilityId'<>''
    ORDER BY w.updated_at,i.id LIMIT 200 FOR SHARE OF i,w`, [effect.space_id, Boolean(effect.payload.task.created_by_agent_id)])).rows;
  for (const target of targets) {
    if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [target.user_id])).rowCount) continue;
    const member = (await tx.query<{ role: string }>("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE", [effect.space_id, target.user_id])).rows[0];
    if (!member) continue;
    const permissions: Record<string, boolean> = await spacePermissions(tx, target.user_id, { id: effect.space_id, role: member.role, is_default: false });
    if (!permissions["agents.run"] || !permissions["tasks.view"]) continue;
    if (!(await tx.query(`SELECT t.id FROM space_tasks t WHERE t.id=$1 AND t.space_id=$2 AND ${taskAudience("$3")}`,
      [effect.task_id, effect.space_id, target.user_id])).rowCount) continue;
    const request = { requesting_member_id: target.user_id, space_id: effect.space_id, agent_id: target.agent_id,
      source_type: "task", capability_id: target.capability_id, trigger_kind: "task_change",
      input: { trigger: { kind: "task_change", provider: "space_tasks", eventId, resourceId: effect.task_id, fingerprint }, event: payload } };
    // A claim is pending downstream work, not a completed workflow. Preserve the
    // member/version/event key and never overwrite an existing Go/native claim.
    await tx.query(`INSERT INTO space_workflow_event_claims(instance_id,workflow_version_id,provider,event_id,fingerprint,state,execution_owner,native_request)
      VALUES($1,$2,'space_tasks',$3,$4,'claimed','hono',$5::jsonb) ON CONFLICT DO NOTHING`,
    [target.id, target.workflow_version_id, eventId, fingerprint, JSON.stringify(request)]);
  }
}
