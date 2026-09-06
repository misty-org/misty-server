import { z } from "zod";
import { clientInteger, SpaceError, trimSpace } from "../../spaces/model.js";

export type RoadmapRow = Record<string, unknown> & { id: string };
export const roadmapColumns = `r.id,r.space_id,r.name,r.description,r.graph_version,r.created_by_user_id,r.audience_kind,r.audience_conversation_id,r.archived_at,r.created_at,r.updated_at`;
export const roadmapAudience = (user: string) => `(r.audience_kind='space' OR (r.audience_kind='conversation' AND EXISTS(
  SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
  WHERE cm.conversation_id=r.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=${user} AND c.space_id=r.space_id)))`;
const optional = ["audience_conversation_id", "archived_at", "target_date", "manual_completed_at", "manual_completed_by_user_id", "milestone_id", "definition_id", "source_goal_id", "target_goal_id"];
export function roadmapResponse(row: RoadmapRow): RoadmapRow {
  const result = { ...row };
  for (const key of ["graph_version", "version", "rank"]) if (key in result) result[key] = clientInteger(result[key] as string);
  for (const key of optional) if (result[key] === null || result[key] === "") delete result[key];
  return result;
}
const text = z.string().nullable().optional().transform(value => trimSpace(value ?? ""));
export function roadmapInput(raw: unknown, update = false) {
  const parsed = z.object({ name: text, description: text, expected_version: z.number().int().safe().positive().optional() }).safeParse(raw);
  if (!parsed.success || !parsed.data.name || [...parsed.data.name].length > 160 || [...parsed.data.description].length > 5000 || update && !parsed.data.expected_version) throw new SpaceError("invalid_request");
  return parsed.data;
}
export function roadmapVersion(raw: string) {
  if (!/^[+]?\d+$/.test(raw) || BigInt(raw) < 1n || BigInt(raw) > 9223372036854775807n) throw new SpaceError("invalid_request");
  return raw;
}
