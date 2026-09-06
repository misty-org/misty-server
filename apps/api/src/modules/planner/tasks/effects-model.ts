import { z } from "zod";
import { trimSpace } from "../../spaces/model.js";

export type TaskEffect = {
  id: string; space_id: string; actor_user_id: string; task_id: string; task_version: string;
  event_kind: "created" | "updated" | "moved" | "archived";
  payload: { task: Record<string, unknown>; agent_run?: unknown };
};
const deviceCapabilities = new Set([
  "files.read", "files.write", "files.list", "files.search", "files.copy", "files.move", "files.delete",
  "project.patch", "project.diff", "project.status", "project.checks", "git.commit", "git.push", "terminal.execute",
  "browser.inspect", "browser.navigate", "browser.click", "browser.type", "browser.select", "browser.scroll",
  "browser.downloads.list", "browser.upload", "browser.confirm_high_risk",
]);
export class TaskEffectInvalid extends Error {}
export function assignmentContext(raw: unknown, defaultMode: string) {
  const parsed = z.object({ mode: z.string().optional(), context_references: z.array(z.object({
    device_id: z.string().transform(trimSpace), kind: z.string().transform(trimSpace).pipe(z.enum(["browser_tab", "project_root"])),
    opaque_ref: z.string().transform(trimSpace), display_name: z.string().default(""),
    capabilities: z.array(z.string().transform(trimSpace)).min(1), metadata: z.unknown().default({}),
  })).max(8).nullable().optional() }).safeParse(raw ?? {});
  if (!parsed.success) throw new TaskEffectInvalid("task_context_invalid");
  const mode = trimSpace(parsed.data.mode ?? "").toLowerCase() || defaultMode;
  if (!["ask", "auto", "full"].includes(mode)) throw new TaskEffectInvalid("task_run_mode_invalid");
  const contexts = parsed.data.context_references ?? [];
  for (const context of contexts) {
    if (!context.device_id || !context.opaque_ref || context.capabilities.some(value => !deviceCapabilities.has(value))) throw new TaskEffectInvalid("task_context_invalid");
    context.capabilities = [...new Set(context.capabilities)];
  }
  return { mode, contexts };
}
