import { randomUUID } from "node:crypto";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { taskAudience } from "../access.js";
import { roadmapVersion, type RoadmapRow } from "./model.js";
import { goalColumns, goalInput, goalResponse, goalTasksInput } from "./child-model.js";
import { roadmapChildNotification, type RoadmapTransactions } from "./transactions.js";
export function createGoalRepository({ mutate }: RoadmapTransactions) {
  return {
    create(actor: SpaceActor, spaceId: string, roadmapId: string, raw: unknown) {
      const input = goalInput(raw, true), id = `goal_${randomUUID()}`;
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        const row = (await tx.query<RoadmapRow>(`INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,description,target_date,rank,position_x,position_y)
          SELECT $1,$2,$3,m.id,$4,$5,$6,(SELECT COALESCE(MAX(rank),0)+1024 FROM space_roadmap_goals WHERE milestone_id=m.id AND archived_at IS NULL),$7,$8
          FROM space_roadmap_milestones m WHERE m.id=$9 AND m.roadmap_id=$3 AND m.space_id=$2 AND m.archived_at IS NULL RETURNING ${goalColumns}`,
        [id, spaceId, roadmapId, input.title, input.description, input.target_date, input.position_x, input.position_y, input.milestone_id])).rows[0];
        if (!row) throw new SpaceError("not_found");
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "goal.created", version); return { goal: goalResponse(row), graph_version: version };
      });
    },
    update(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, raw: unknown) {
      const input = goalInput(raw);
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        // Lock linked tasks before the goal, matching task-status trigger order.
        if (input.complete_manually === true) {
          const linked = await tx.query<{ status: string; archived_at: Date | null }>(`SELECT t.id,t.status,t.archived_at FROM space_roadmap_goal_tasks gt JOIN space_tasks t ON t.id=gt.task_id AND t.space_id=gt.space_id
            WHERE gt.goal_id=$1 AND gt.roadmap_id=$2 AND gt.space_id=$3 FOR SHARE OF t`, [id, roadmapId, spaceId]);
          if (linked.rows.some(task => !task.archived_at && task.status !== "canceled")) throw new SpaceError("invalid_request");
        }
        if (!(await tx.query("SELECT id FROM space_roadmap_milestones WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [input.milestone_id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        const row = (await tx.query<RoadmapRow>(`UPDATE space_roadmap_goals SET milestone_id=$4,title=$5,description=$6,target_date=$7,
          rank=CASE WHEN $8>0 THEN $8 ELSE rank END,
          manual_completed_at=CASE WHEN $9::boolean IS NULL THEN manual_completed_at WHEN $9 THEN now() ELSE NULL END,
          manual_completed_by_user_id=CASE WHEN $9::boolean IS NULL THEN manual_completed_by_user_id WHEN $9 THEN $10 ELSE NULL END,
          version=version+1,updated_at=now() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL RETURNING ${goalColumns}`,
        [id, roadmapId, spaceId, input.milestone_id, input.title, input.description, input.target_date, input.rank, input.complete_manually ?? null, actor.userId])).rows[0];
        if (!row) throw new SpaceError("not_found");
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "goal.updated", version); return { goal: goalResponse(row), graph_version: version };
      });
    },
    archive(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, expected: string) {
      roadmapVersion(expected);
      return mutate(actor, spaceId, roadmapId, expected, async (tx, version) => {
        if (!(await tx.query("UPDATE space_roadmap_goals SET archived_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "goal.archived", version); return { graph_version: version };
      });
    },
    setTasks(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, raw: unknown) {
      const input = goalTasksInput(raw);
      return mutate(actor, spaceId, roadmapId, input.expected, async (tx, version) => {
        if (!(await tx.query("SELECT id FROM space_roadmap_goals WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        if (input.ids.length) {
          const tasks = await tx.query(`SELECT t.id FROM space_tasks t WHERE t.id=ANY($1::text[]) AND t.space_id=$2 AND t.archived_at IS NULL AND ${taskAudience("$3")} ORDER BY t.id FOR SHARE OF t`, [input.ids, spaceId, actor.userId]);
          if (tasks.rowCount !== input.ids.length) throw new SpaceError("invalid_request");
        }
        await tx.query("DELETE FROM space_roadmap_goal_tasks WHERE goal_id=$1 AND roadmap_id=$2 AND space_id=$3", [id, roadmapId, spaceId]);
        await tx.query("INSERT INTO space_roadmap_goal_tasks(space_id,roadmap_id,goal_id,task_id,added_by_user_id) SELECT $1,$2,$3,task_id,$4 FROM unnest($5::text[]) AS task_id", [spaceId, roadmapId, id, actor.userId, input.ids]);
        if (input.ids.length) await tx.query("UPDATE space_roadmap_goals SET manual_completed_at=NULL,manual_completed_by_user_id=NULL,version=version+1,updated_at=now() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3", [id, roadmapId, spaceId]);
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "goal.tasks.updated", version); return { graph_version: version };
      });
    },
  };
}
