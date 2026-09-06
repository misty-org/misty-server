import { z } from "zod";
import { SpaceTaskAgentRunInputSchema } from "@misty/contracts";
import { SpaceError, trimSpace } from "../../spaces/model.js";

const text = z.string().nullable().optional().transform(value => value ?? "");
const taskInput = z.object({ title: text, notes: text, status: text, priority: text,
  assignee_user_id: text, assignee_agent_id: text, due_timezone: text,
  due_at: z.iso.datetime({ offset: true }).nullable().optional(),
  source_refs: z.array(z.record(z.string(), z.unknown())).max(20).nullable().optional(),
  agent_run: SpaceTaskAgentRunInputSchema.nullable().optional(),
  version: z.number().int().safe().positive().optional(),
});
export type TaskWrite = ReturnType<typeof normalizeTaskWrite>;
export function normalizeTaskWrite(raw: unknown, creating: boolean) {
  const parsed = taskInput.safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request");
  const input = parsed.data;
  if (!creating && !input.version) throw new SpaceError("invalid_request");
  if (creating && input.status === "") input.status = "todo";
  for (const key of ["title", "notes", "status", "priority", "due_timezone"] as const) input[key] = trimSpace(input[key]);
  input.priority ||= "medium"; input.due_timezone ||= "UTC";
  if ([...input.title].length < 1 || [...input.title].length > 240 || [...input.notes].length > 20000
    || !["todo", "in_progress", "done", "canceled"].includes(input.status) || !["high", "medium", "low"].includes(input.priority)
    || input.assignee_user_id && input.assignee_agent_id) throw new SpaceError("invalid_request");
  try { new Intl.DateTimeFormat("en", { timeZone: input.due_timezone }); } catch { throw new SpaceError("invalid_request"); }
  for (const ref of input.source_refs ?? []) {
    if (ref.kind != null && typeof ref.kind !== "string" || ref.resource_id != null && typeof ref.resource_id !== "string") throw new SpaceError("invalid_request");
    const kind = typeof ref.kind === "string" ? ref.kind : "";
    if (kind && (!["library_item", "task_attachment", "chat_attachment"].includes(kind)
      || typeof ref.resource_id !== "string" || !trimSpace(ref.resource_id))) throw new SpaceError("invalid_request");
  }
  return { ...input, due_at: input.due_at ?? null, source_refs: input.source_refs === undefined ? [] : input.source_refs };
}
export function normalizeTaskMove(raw: unknown) {
  const parsed = z.object({ version: z.number().int().safe().positive(), status: z.enum(["todo", "in_progress", "done", "canceled"]),
    before_task_id: text }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request"); return parsed.data;
}
