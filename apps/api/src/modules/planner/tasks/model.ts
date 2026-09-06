import { z } from "zod";
import { clientInteger, SpaceError, trimSpace } from "../../spaces/model.js";

export const taskColumns = `t.id,t.space_id,t.task_number,t.task_key,t.title,t.notes,t.status,t.priority,t.rank,t.assignee_user_id,t.assignee_agent_id,
  t.due_at,t.due_timezone,t.source_refs,t.created_by_user_id,t.created_by_agent_id,t.source_run_id,t.audience_kind,t.audience_conversation_id,
  t.audience_creator_user_id,t.version,t.completed_at,t.archived_at,t.created_at,t.updated_at,t.schedule,t.calendar,t.conflicted_fields`;
export function taskResponse(row: Record<string, unknown>) {
  const output = { ...row };
  for (const key of ["task_number", "rank", "version"]) output[key] = clientInteger(row[key] as string);
  for (const key of ["assignee_user_id", "assignee_agent_id", "created_by_user_id", "created_by_agent_id", "source_run_id", "audience_conversation_id", "audience_creator_user_id",
    "due_at", "completed_at", "archived_at", "schedule", "calendar"]) if (output[key] === null || output[key] === "") delete output[key];
  if (!Array.isArray(output.conflicted_fields) || !output.conflicted_fields.length) delete output.conflicted_fields;
  return output;
}
export function taskCursor(raw: string) {
  if (!raw) return 0;
  // Go's raw URL decoder permits CR/LF but not padding or standard-base64 characters.
  const token = raw.replace(/[\r\n]/g, "");
  if (!/^[A-Za-z0-9_-]+$/.test(token) || token.length % 4 === 1 || token.length > 64) throw new SpaceError("invalid_request");
  const value = Buffer.from(token, "base64url").toString("utf8");
  if (!/^[+-]?\d+$/.test(value)) throw new SpaceError("invalid_request");
  const offset = Number(value); if (!Number.isSafeInteger(offset) || offset < 0 || offset > 1_000_000) throw new SpaceError("invalid_request"); return offset;
}
export function taskQuery(raw: Record<string, string | undefined>) {
  const date = (value: string | undefined) => {
    if (!value || !trimSpace(value)) return null;
    if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(value) || !z.iso.datetime({ offset: true }).safeParse(value).success) throw new SpaceError("invalid_request"); return value;
  };
  const priority = trimSpace(raw.priority ?? ""), sort = raw.sort ?? "rank";
  if (priority && !["high", "medium", "low"].includes(priority) || !["", "rank", "due", "updated"].includes(sort)) throw new SpaceError("invalid_request");
  const requestedLimit = /^[+-]?\d+$/.test(raw.limit ?? "") ? Number(raw.limit) : 0;
  return { status: trimSpace(raw.status ?? ""), assigneeUserId: trimSpace(raw.assignee_user_id ?? ""), assigneeAgentId: trimSpace(raw.assignee_agent_id ?? ""),
    priority, search: trimSpace(raw.q ?? ""), dueFrom: date(raw.due_from), dueTo: date(raw.due_to), sort, offset: taskCursor(raw.cursor ?? ""),
    limit: requestedLimit >= 1 && requestedLimit <= 200 ? requestedLimit : 100, includeArchived: raw.include_archived === "true" };
}
