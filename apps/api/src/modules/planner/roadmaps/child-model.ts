import { z } from "zod";
import { SpaceError, trimSpace } from "../../spaces/model.js";
import { roadmapResponse, type RoadmapRow } from "./model.js";
const text = z.string().nullable().optional().transform(value => trimSpace(value ?? ""));
const number = z.number().finite().nullable().optional().transform(value => value ?? 0);
const base = z.object({ title: text, description: text, target_date: z.iso.datetime({ offset: true }).nullable().optional(),
  rank: z.number().int().safe().nullable().optional().transform(value => value ?? 0), position_x: number, position_y: number,
  expected_version: z.number().int().safe().positive() });
export const milestoneColumns = `id,space_id,roadmap_id,title,description,target_date::timestamp AT TIME ZONE 'UTC' AS target_date,rank,position_x,position_y,width,height,version,created_at,updated_at`;
export const goalColumns = `id,space_id,roadmap_id,milestone_id,title,description,target_date::timestamp AT TIME ZONE 'UTC' AS target_date,rank,position_x,position_y,manual_completed_at,manual_completed_by_user_id,version,created_at,updated_at`;
export function milestoneInput(raw: unknown, create = false) {
  const parsed = base.extend({ width: number, height: number }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request"); const value = parsed.data;
  if (!value.title || [...value.title].length > 200 || [...value.description].length > 10000) throw new SpaceError("invalid_request");
  value.width ||= 440; value.height ||= 360;
  if (create && (Math.abs(value.position_x) > 10000000 || Math.abs(value.position_y) > 10000000 || value.width < 280 || value.width > 2400 || value.height < 220 || value.height > 2400)) throw new SpaceError("invalid_request");
  return { ...value, target_date: value.target_date?.slice(0, 10) ?? null };
}
export function goalInput(raw: unknown, create = false) {
  const parsed = base.extend({ milestone_id: z.string().min(1), complete_manually: z.boolean().nullable().optional() }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request"); const value = parsed.data;
  if (!value.title || [...value.title].length > 240 || [...value.description].length > 20000 || create && (Math.abs(value.position_x) > 10000000 || Math.abs(value.position_y) > 10000000)) throw new SpaceError("invalid_request");
  return { ...value, target_date: value.target_date?.slice(0, 10) ?? null };
}
export const milestoneResponse = (row: RoadmapRow) => ({ ...roadmapResponse(row), goal_total: 0, goal_done: 0, status: "" });
export const goalResponse = (row: RoadmapRow) => ({ ...roadmapResponse(row), task_total: 0, task_done: 0, progress_percentage: 0, status: "", tasks: [] });
export function goalTasksInput(raw: unknown) {
  const parsed = z.object({ task_ids: z.array(z.string()).max(100).nullable().optional(), expected_version: z.number().int().safe().positive() }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request");
  const ids = (parsed.data.task_ids ?? []).map(trimSpace).sort();
  if (ids.some(id => !id) || new Set(ids).size !== ids.length) throw new SpaceError("invalid_request");
  return { ids, expected: parsed.data.expected_version };
}
