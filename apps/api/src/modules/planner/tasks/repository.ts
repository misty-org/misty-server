import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../../spaces/access.js";
import { clientInteger, SpaceError } from "../../spaces/model.js";
import { requirePlannerActor, taskAudience } from "../access.js";
import { taskColumns, taskQuery, taskResponse } from "./model.js";
import { createTaskMutations } from "./mutations.js";

export function createTaskRepository(pool: Pool) {
  return {
    ...createTaskMutations(pool),
    list: (actor: SpaceActor, spaceId: string, raw: Record<string, string | undefined>) => withTransaction(pool, async (tx) => {
      const query = taskQuery(raw);
      await requirePlannerActor(tx, actor, spaceId, "tasks.read");
      // Let PostgreSQL compare the original timestamps, preserving sub-millisecond
      // query bounds that a JavaScript Date would truncate.
      if (query.dueFrom && query.dueTo && !(await tx.query<{ valid: boolean }>("SELECT $2::timestamptz>$1::timestamptz AS valid", [query.dueFrom, query.dueTo])).rows[0]!.valid) throw new SpaceError("invalid_request");
      const order = query.sort === "due" ? "t.due_at NULLS LAST,t.updated_at DESC,t.id" : query.sort === "updated" ? "t.updated_at DESC,t.id" : "t.status,t.rank,t.id";
      const rows = (await tx.query(`SELECT ${taskColumns} FROM space_tasks t WHERE t.space_id=$1
        AND ($2='' OR t.status=$2) AND ($3='' OR t.assignee_user_id=$3) AND ($4='' OR t.assignee_agent_id=$4)
        AND ($5='' OR t.priority=$5) AND ($6='' OR t.title ILIKE '%'||$6||'%' OR t.notes ILIKE '%'||$6||'%' OR t.task_key ILIKE '%'||$6||'%')
        AND ($7::timestamptz IS NULL OR t.due_at>=$7) AND ($8::timestamptz IS NULL OR t.due_at<$8)
        AND ($9 OR t.archived_at IS NULL) AND ${taskAudience("$12")} ORDER BY ${order} LIMIT $10 OFFSET $11`,
        [spaceId, query.status, query.assigneeUserId, query.assigneeAgentId, query.priority, query.search, query.dueFrom, query.dueTo,
          query.includeArchived, query.limit + 1, query.offset, actor.userId])).rows;
      const totals: Record<string, number> = { todo: 0, in_progress: 0, done: 0, canceled: 0 };
      for (const row of (await tx.query<{ status: string; count: string }>(`SELECT t.status,count(*) FROM space_tasks t
        WHERE t.space_id=$1 AND t.archived_at IS NULL AND ${taskAudience("$2")} GROUP BY t.status`, [spaceId, actor.userId])).rows) totals[row.status] = clientInteger(row.count);
      return { tasks: rows.slice(0, query.limit).map(taskResponse), status_totals: totals,
        ...(rows.length > query.limit ? { next_cursor: Buffer.from(String(query.offset + query.limit)).toString("base64url") } : {}) };
    }, { mode: "service" }),
    activity: (actor: SpaceActor, spaceId: string, taskId: string) => withTransaction(pool, async (tx) => {
      await requirePlannerActor(tx, actor, spaceId, "tasks.read");
      if (!(await tx.query(`SELECT t.id FROM space_tasks t WHERE t.space_id=$1 AND t.id=$2 AND ${taskAudience("$3")} FOR SHARE OF t`, [spaceId, taskId, actor.userId])).rowCount) throw new SpaceError("not_found");
      const rows = (await tx.query(`SELECT id,space_id,task_id,actor_kind,actor_user_id,actor_agent_id,run_id,kind,message,metadata,created_at
        FROM space_task_activity WHERE task_id=$1 AND space_id=$2 ORDER BY created_at,id`, [taskId, spaceId])).rows;
      return { activity: rows.map((row) => {
        for (const key of ["actor_user_id", "actor_agent_id", "run_id"]) if (!row[key]) delete row[key]; return row;
      }) };
    }, { mode: "service" }),
  };
}
