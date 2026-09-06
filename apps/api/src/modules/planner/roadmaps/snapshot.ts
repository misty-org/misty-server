import type { PoolClient } from "pg";
import { SpaceError } from "../../spaces/model.js";
import { taskAudience } from "../access.js";
import { taskColumns, taskResponse } from "../tasks/model.js";
import { roadmapAudience, roadmapColumns, roadmapResponse, type RoadmapRow } from "./model.js";

type Milestone = RoadmapRow & { goal_total: number; goal_done: number; status: string };
type Goal = RoadmapRow & { milestone_id: string; task_total: number; task_done: number; progress_percentage: number; status: string; tasks: Record<string, unknown>[] };
export async function loadRoadmapSnapshot(tx: PoolClient, userId: string, spaceId: string, roadmapId: string) {
  const row = (await tx.query<RoadmapRow>(`SELECT ${roadmapColumns} FROM space_roadmaps r
    WHERE r.id=$1 AND r.space_id=$2 AND r.archived_at IS NULL AND ${roadmapAudience("$3")}`, [roadmapId, spaceId, userId])).rows[0];
  if (!row) throw new SpaceError("not_found");
  const milestones: Milestone[] = (await tx.query<RoadmapRow>(`SELECT id,space_id,roadmap_id,title,description,target_date::timestamp AT TIME ZONE 'UTC' AS target_date,
    rank,position_x,position_y,width,height,version,created_at,updated_at FROM space_roadmap_milestones
    WHERE roadmap_id=$1 AND space_id=$2 AND archived_at IS NULL ORDER BY rank,id`, [roadmapId, spaceId])).rows
    .map(row => ({ ...roadmapResponse(row), goal_total: 0, goal_done: 0, status: "not_started" }));
  const goals: Goal[] = (await tx.query<RoadmapRow & { milestone_id: string }>(`SELECT id,space_id,roadmap_id,milestone_id,title,description,
    target_date::timestamp AT TIME ZONE 'UTC' AS target_date,rank,position_x,position_y,manual_completed_at,manual_completed_by_user_id,version,created_at,updated_at
    FROM space_roadmap_goals WHERE roadmap_id=$1 AND space_id=$2 AND archived_at IS NULL ORDER BY milestone_id,rank,id`, [roadmapId, spaceId])).rows
    .map(row => ({ ...roadmapResponse(row), milestone_id: row.milestone_id, task_total: 0, task_done: 0, progress_percentage: 0, status: "not_started", tasks: [] }));
  const nodes = (await tx.query<RoadmapRow>(`SELECT id,space_id,roadmap_id,milestone_id,definition_id,node_kind,title,description,
    target_date::timestamp AT TIME ZONE 'UTC' AS target_date,position_x,position_y,field_values,version,archived_at,created_at,updated_at
    FROM space_roadmap_nodes WHERE roadmap_id=$1 AND space_id=$2 AND archived_at IS NULL ORDER BY created_at,id`, [roadmapId, spaceId])).rows.map(roadmapResponse);
  const definitions = (await tx.query<RoadmapRow>(`SELECT d.id,d.space_id,d.name,d.description,d.icon,d.color,d.agenda_visible,d.field_schema,d.version,d.created_by_user_id,d.archived_at,d.created_at,d.updated_at
    FROM space_roadmap_node_definitions d WHERE d.space_id=$1 AND (d.archived_at IS NULL OR EXISTS(
      SELECT 1 FROM space_roadmap_nodes n WHERE n.definition_id=d.id AND n.roadmap_id=$2 AND n.archived_at IS NULL)) ORDER BY d.name,d.id`, [spaceId, roadmapId])).rows.map(roadmapResponse);
  const visible = new Set([...milestones.map(item => `milestone:${item.id}`), ...goals.map(item => `goal:${item.id}`), ...nodes.map(item => `node:${item.id}`)]);
  const edges = (await tx.query<RoadmapRow & { source_kind: string; source_id: string; target_kind: string; target_id: string }>(`SELECT id,space_id,roadmap_id,
    source_kind,source_id,target_kind,target_id,source_goal_id,target_goal_id,edge_type,label,version,created_at,updated_at
    FROM space_roadmap_edges WHERE roadmap_id=$1 AND space_id=$2 ORDER BY created_at,id`, [roadmapId, spaceId])).rows
    .filter(item => visible.has(`${item.source_kind}:${item.source_id}`) && visible.has(`${item.target_kind}:${item.target_id}`))
    .map(({ source_kind, source_id, target_kind, target_id, ...row }) => ({ ...roadmapResponse(row), source: { kind: source_kind, id: source_id }, target: { kind: target_kind, id: target_id } }));
  const goalsById = new Map(goals.map(goal => [goal.id, goal]));
  if (goals.length) {
    const tasks = (await tx.query<RoadmapRow & { goal_id: string }>(`SELECT gt.goal_id,${taskColumns} FROM space_tasks t
      JOIN space_roadmap_goal_tasks gt ON gt.task_id=t.id AND gt.space_id=t.space_id
      WHERE gt.roadmap_id=$1 AND gt.space_id=$2 AND ${taskAudience("$3")} ORDER BY gt.added_at,gt.task_id`, [roadmapId, spaceId, userId])).rows;
    for (const { goal_id, ...task } of tasks) goalsById.get(goal_id)?.tasks.push(taskResponse(task));
  }
  const milestoneMap = new Map(milestones.map(item => [item.id, item])); let goalDone = 0, milestoneDone = 0;
  for (const goal of goals) {
    const active = goal.tasks.filter(task => !task.archived_at && task.status !== "canceled");
    goal.task_total = active.length; goal.task_done = active.filter(task => task.status === "done").length;
    if (!goal.task_total) { if (goal.manual_completed_at) { goal.status = "done"; goal.progress_percentage = 100; } }
    else {
      goal.progress_percentage = Math.floor((goal.task_done * 100 + Math.floor(goal.task_total / 2)) / goal.task_total);
      goal.status = goal.task_done === goal.task_total ? "done" : active.some(task => task.status === "done" || task.status === "in_progress") ? "in_progress" : "not_started";
    }
    if (goal.status === "done") goalDone++;
    const milestone = milestoneMap.get(goal.milestone_id);
    if (milestone) { milestone.goal_total++; if (goal.status === "done") milestone.goal_done++; }
  }
  for (const milestone of milestones) {
    if (milestone.goal_total > 0 && milestone.goal_done === milestone.goal_total) { milestone.status = "done"; milestoneDone++; }
    else if (milestone.goal_done > 0) milestone.status = "in_progress";
  }
  return { roadmap: roadmapResponse(row), milestones, goals, nodes, node_definitions: definitions, edges,
    goal_total: goals.length, goal_done: goalDone, milestone_total: milestones.length, milestone_done: milestoneDone,
    progress_percentage: goals.length ? Math.floor((goalDone * 100 + Math.floor(goals.length / 2)) / goals.length) : 0 };
}
