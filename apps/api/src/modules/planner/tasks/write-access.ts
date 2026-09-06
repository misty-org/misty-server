import type { PoolClient } from "pg";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { taskAudience } from "../access.js";
import { taskColumns } from "./model.js";
import type { TaskWrite } from "./input.js";

export async function lockTask(tx: PoolClient, actor: SpaceActor, spaceId: string, taskId: string, archived = false) {
  const row = (await tx.query(`SELECT ${taskColumns} FROM space_tasks t WHERE t.id=$1 AND t.space_id=$2
    AND ${taskAudience("$3")} FOR UPDATE OF t`, [taskId, spaceId, actor.userId])).rows[0];
  if (!row || !archived && row.archived_at) throw new SpaceError("not_found");
  return row;
}
export async function validateTaskReferences(tx: PoolClient, actor: SpaceActor, spaceId: string, taskId: string,
  input: TaskWrite, permissions: Record<string, boolean>) {
  if (input.assignee_user_id && !(await tx.query("SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE", [spaceId, input.assignee_user_id])).rowCount) throw new SpaceError("invalid_request");
  if (input.assignee_agent_id && !(await tx.query(`SELECT a.id FROM personal_agents a
    WHERE a.id=$1 AND a.owner_user_id=$2 AND a.enabled AND a.deleted_at IS NULL
    AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=$3 AND m.user_id=a.owner_user_id) FOR SHARE OF a`,
  [input.assignee_agent_id, actor.userId, spaceId])).rowCount) throw new SpaceError("invalid_request");
  for (const ref of input.source_refs ?? []) {
    if (!ref.kind) continue;
    if (ref.kind === "library_item") {
      if (!permissions["library.view"]) throw new SpaceError("forbidden");
      if (!(await tx.query("SELECT id FROM space_library_items WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready' AND NOT hidden", [ref.resource_id, spaceId])).rowCount) throw new SpaceError("not_found");
    } else if (ref.kind === "chat_attachment") {
      if (!permissions["messages.read"]) throw new SpaceError("forbidden");
      if (!(await tx.query(`SELECT a.id FROM space_message_attachments a WHERE a.id=$1 AND a.space_id=$2
        AND a.message_id IS NOT NULL AND a.lifecycle_state='ready'`, [ref.resource_id, spaceId])).rowCount) throw new SpaceError("not_found");
    } else if (ref.kind === "task_attachment") {
      if (!(await tx.query(`SELECT a.id FROM space_message_attachments a WHERE a.id=$1 AND a.space_id=$2 AND a.message_id IS NULL
        AND a.lifecycle_state='ready' AND (a.uploader_user_id=$3 OR EXISTS(SELECT 1 FROM space_tasks t WHERE t.id=$4 AND t.space_id=$2
        AND t.source_refs @> jsonb_build_array(jsonb_build_object('kind','task_attachment','resource_id',$1::text))))`,
      [ref.resource_id, spaceId, actor.userId, taskId])).rowCount) throw new SpaceError("not_found");
    }
  }
}
export async function cancelPreviousAssignment(tx: PoolClient, taskId: string, agentId: string) {
  await tx.query(`WITH canceled AS (
    UPDATE space_runs SET state='canceled',runtime_phase='canceled',error_code='task_unassigned',canceled_at=now(),completed_at=now(),updated_at=now()
    WHERE source_task_id=$1 AND agent_id=$2 AND state IN ('queued','running','cooldown','awaiting_approval','awaiting_device') RETURNING id
  ) UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=now(),updated_at=now()
    WHERE run_id IN (SELECT id FROM canceled) AND state IN ('queued','leased','dispatched')`, [taskId, agentId]);
  const runs = "SELECT id FROM space_runs WHERE source_task_id=$1 AND agent_id=$2 AND state='canceled' AND error_code='task_unassigned'";
  await tx.query(`UPDATE agent_run_tool_approvals SET state='denied',decided_at=now() WHERE run_id IN (${runs}) AND state='pending'`, [taskId, agentId]);
  await tx.query(`UPDATE agent_run_contexts SET state='detached',updated_at=now() WHERE run_id IN (${runs}) AND state='attached'`, [taskId, agentId]);
}
