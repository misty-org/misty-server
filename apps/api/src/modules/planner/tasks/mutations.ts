import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { requirePlannerActor, taskAudience } from "../access.js";
import { taskColumns, taskResponse } from "./model.js";
import { normalizeTaskMove, normalizeTaskWrite, type TaskWrite } from "./input.js";
import { cancelPreviousAssignment, lockTask, validateTaskReferences } from "./write-access.js";

async function recordMutation(tx: PoolClient, actor: SpaceActor, row: Record<string, unknown>, kind: string, input?: TaskWrite) {
  const task = taskResponse(row);
  const event = (await tx.query<{ id: string }>(`INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
    VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id`, [row.space_id, `task.${kind}`, actor.userId, row.id,
    JSON.stringify(kind === "archived" ? { version: task.version } : { task })])).rows[0]!;
  await tx.query(`INSERT INTO native_task_effects(id,space_id,actor_user_id,task_id,event_kind,task_version,payload)
    VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, [event.id, row.space_id, actor.userId, row.id, kind, row.version,
    JSON.stringify({ task, agent_run: input?.agent_run ?? null })]);
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
  return task;
}
async function assignmentActivity(tx: PoolClient, actor: SpaceActor, row: Record<string, unknown>) {
  await tx.query(`INSERT INTO space_task_activity(id,space_id,task_id,actor_kind,actor_user_id,kind,message,metadata)
    VALUES($1,$2,$3,'person',$4,'assigned','Assigned to Agent',$5::jsonb)`, [randomUUID(), row.space_id, row.id, actor.userId,
    JSON.stringify({ agent_id: row.assignee_agent_id, task_version: taskResponse(row).version })]);
}
export function createTaskMutations(pool: Pool) {
  async function write<T>(actor: SpaceActor, spaceId: string, operation: (tx: PoolClient, permissions: Record<string, boolean>) => Promise<T>) {
    return withTransaction(pool, async tx => {
      await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
      const permissions = await requirePlannerActor(tx, actor, spaceId, "tasks.write", true);
      // Include all destinations in stable order: assigning an Agent can change
      // todo to in_progress after the prior assignment is read. Use Go's lock keys.
      for (const status of ["canceled", "done", "in_progress", "todo"]) {
        await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`space-task-rank:${spaceId}:${status}`]);
      }
      return operation(tx, permissions);
    }, { mode: "service" });
  }
  async function rankBefore(tx: PoolClient, actor: SpaceActor, spaceId: string, taskId: string, status: string, beforeId: string): Promise<bigint> {
    if (!beforeId) return BigInt((await tx.query<{ rank: string }>(`SELECT (COALESCE(MAX(rank),0)+1024)::text AS rank
      FROM space_tasks WHERE space_id=$1 AND status=$2 AND id<>$3 AND archived_at IS NULL`, [spaceId, status, taskId])).rows[0]!.rank);
    const before = (await tx.query<{ rank: string }>(`SELECT t.rank::text FROM space_tasks t WHERE t.id=$1 AND t.space_id=$2
      AND t.status=$3 AND t.id<>$4 AND t.archived_at IS NULL AND ${taskAudience("$5")}`, [beforeId, spaceId, status, taskId, actor.userId])).rows[0];
    if (!before) throw new SpaceError("invalid_request");
    const previous = (await tx.query<{ rank: string }>(`SELECT COALESCE(MAX(rank),0)::text AS rank FROM space_tasks
      WHERE space_id=$1 AND status=$2 AND id<>$3 AND rank<$4 AND archived_at IS NULL`, [spaceId, status, taskId, before.rank])).rows[0]!;
    const gap = BigInt(before.rank) - BigInt(previous.rank);
    return gap <= 1n ? 0n : BigInt(previous.rank) + gap / 2n;
  }
  return {
    create(actor: SpaceActor, spaceId: string, raw: unknown) {
      const input = normalizeTaskWrite(raw, true), id = `task_${randomUUID()}`;
      return write(actor, spaceId, async (tx, permissions) => {
        await validateTaskReferences(tx, actor, spaceId, id, input, permissions);
        if (input.assignee_agent_id && input.status === "todo") input.status = "in_progress";
        const counter = (await tx.query<{ last_number: string }>(`INSERT INTO space_task_counters(space_id,last_number) VALUES($1,1)
          ON CONFLICT(space_id) DO UPDATE SET last_number=space_task_counters.last_number+1 RETURNING last_number`, [spaceId])).rows[0]!;
        const rank = await rankBefore(tx, actor, spaceId, id, input.status, "");
        const row = (await tx.query(`INSERT INTO space_tasks AS t(id,space_id,task_number,task_key,title,notes,status,priority,rank,
          assignee_user_id,assignee_agent_id,due_at,due_timezone,source_refs,created_by_user_id,audience_kind,completed_at)
          VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12,$13,$14::jsonb,$15,'space',CASE WHEN $7='done' THEN now() END)
          RETURNING ${taskColumns}`, [id, spaceId, counter.last_number, `MST-${counter.last_number}`, input.title, input.notes,
          input.status, input.priority, rank.toString(), input.assignee_user_id, input.assignee_agent_id, input.due_at, input.due_timezone,
          JSON.stringify(input.source_refs), actor.userId])).rows[0]!;
        if (input.assignee_agent_id) await assignmentActivity(tx, actor, row);
        return recordMutation(tx, actor, row, "created", input);
      });
    },
    update(actor: SpaceActor, spaceId: string, taskId: string, raw: unknown) {
      const input = normalizeTaskWrite(raw, false);
      return write(actor, spaceId, async (tx, permissions) => {
        const prior = await lockTask(tx, actor, spaceId, taskId);
        await validateTaskReferences(tx, actor, spaceId, taskId, input, permissions);
        const assignmentChanged = (prior.assignee_agent_id ?? "") !== input.assignee_agent_id;
        if (assignmentChanged && prior.assignee_agent_id) await cancelPreviousAssignment(tx, taskId, prior.assignee_agent_id);
        if (assignmentChanged && input.assignee_agent_id && input.status === "todo") input.status = "in_progress";
        const rank = prior.status === input.status ? prior.rank : (await rankBefore(tx, actor, spaceId, taskId, input.status, "")).toString();
        // Match existing last-write-wins behavior. version is required on the
        // wire, but a stale version does not prevent a write to an active task.
        const row = (await tx.query(`UPDATE space_tasks AS t SET title=$3,notes=$4,status=$5,priority=$6,rank=$7,
          assignee_user_id=NULLIF($8,''),assignee_agent_id=NULLIF($9,''),due_at=$10,due_timezone=$11,source_refs=$12::jsonb,
          completed_at=CASE WHEN $5='done' THEN COALESCE(completed_at,now()) ELSE NULL END,version=version+1,updated_at=now()
          WHERE id=$1 AND space_id=$2 AND archived_at IS NULL RETURNING ${taskColumns}`,
        [taskId, spaceId, input.title, input.notes, input.status, input.priority, rank, input.assignee_user_id,
          input.assignee_agent_id, input.due_at, input.due_timezone, JSON.stringify(input.source_refs)])).rows[0]!;
        if (assignmentChanged && input.assignee_agent_id) await assignmentActivity(tx, actor, row);
        return recordMutation(tx, actor, row, "updated", input);
      });
    },
    archive(actor: SpaceActor, spaceId: string, taskId: string, version: string) {
      if (!/^[+]?[0-9]+$/.test(version) || BigInt(version) < 1n || BigInt(version) > 9223372036854775807n) throw new SpaceError("invalid_request");
      return write(actor, spaceId, async tx => {
        const prior = await lockTask(tx, actor, spaceId, taskId, true);
        if (prior.archived_at) return taskResponse(prior);
        const row = (await tx.query(`UPDATE space_tasks AS t SET archived_at=now(),version=version+1,updated_at=now()
          WHERE id=$1 AND space_id=$2 RETURNING ${taskColumns}`, [taskId, spaceId])).rows[0]!;
        return recordMutation(tx, actor, row, "archived");
      });
    },
    move(actor: SpaceActor, spaceId: string, taskId: string, raw: unknown) {
      const input = normalizeTaskMove(raw);
      return write(actor, spaceId, async tx => {
        await lockTask(tx, actor, spaceId, taskId);
        let rank = await rankBefore(tx, actor, spaceId, taskId, input.status, input.before_task_id);
        if (rank === 0n) {
          await tx.query(`WITH ranked AS (SELECT id,ROW_NUMBER() OVER(ORDER BY rank,id)*1024 AS next_rank
            FROM space_tasks WHERE space_id=$1 AND status=$2 AND id<>$3 AND archived_at IS NULL)
            UPDATE space_tasks t SET rank=ranked.next_rank FROM ranked WHERE t.id=ranked.id`, [spaceId, input.status, taskId]);
          rank = await rankBefore(tx, actor, spaceId, taskId, input.status, input.before_task_id);
          if (rank === 0n) throw new SpaceError("version_conflict");
        }
        const row = (await tx.query(`UPDATE space_tasks AS t SET status=$3,rank=$4,
          completed_at=CASE WHEN $3='done' THEN COALESCE(completed_at,now()) ELSE NULL END,version=version+1,updated_at=now()
          WHERE id=$1 AND space_id=$2 AND archived_at IS NULL RETURNING ${taskColumns}`, [taskId, spaceId, input.status, rank.toString()])).rows[0]!;
        const task = await recordMutation(tx, actor, row, "moved");
        const reordered = (await tx.query(`SELECT ${taskColumns} FROM space_tasks t WHERE t.space_id=$1 AND t.status=$2
          AND t.archived_at IS NULL AND ${taskAudience("$3")} ORDER BY t.rank,t.id`, [spaceId, input.status, actor.userId])).rows.map(taskResponse);
        return { task, reordered };
      });
    },
  };
}
