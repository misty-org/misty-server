import type { ModelMessage } from "ai";

// Pushed page state (docs/plans/agent-harness-v2.md, change 1). After screen_open the agent
// spent a whole turn asking for the new page with browser_snapshot: 39 of 157 calls in one run.
// browser-use shows the page with every step instead. So after a turn that opened tabs, the
// runtime takes the newest ones' snapshots itself and adds them to the transcript, as if the
// agent had asked, and the next turn acts on them directly.

const MAX_PAGES = 3;
/** The same size as the snapshot after browser_steps (src-tauri browser_page AFTER_STEPS_SNAPSHOT_CHARS). */
export const PAGE_STATE_CHARS = 12_000;

function parse(output: unknown, depth = 0): Record<string, unknown> | undefined {
  if (depth > 4 || output === null || output === undefined) return undefined;
  if (typeof output === "string") {
    if (!output.includes("scopeId")) return undefined;
    try {
      return parse(JSON.parse(output), depth + 1);
    } catch {
      return undefined;
    }
  }
  if (typeof output !== "object" || Array.isArray(output)) return undefined;
  const value = output as Record<string, unknown>;
  if (value.status === "open" && typeof value.scopeId === "string") return value;
  for (const key of ["value", "output", "result"]) {
    const nested = parse(value[key], depth + 1);
    if (nested) return nested;
  }
  return undefined;
}

/** The scopes of tabs this turn's calls opened (not tabs found already open), newest last. */
export function openedScopes(results: ReadonlyArray<{ toolName?: string; output?: unknown }> | undefined, openTool: string): string[] {
  const scopes: string[] = [];
  for (const result of results ?? []) {
    if (result.toolName !== openTool) continue;
    const opened = parse(result.output);
    const scope = opened?.scopeId as string | undefined;
    const reused = typeof opened?.message === "string" && opened.message.includes("is already open");
    if (scope && !reused && !scopes.includes(scope)) scopes.push(scope);
  }
  return scopes.slice(-MAX_PAGES);
}

/** The transcript entries for a snapshot the runtime took for the agent. */
export function pageStateMessages(callId: string, snapshotTool: string, scopeId: string, state: unknown): ModelMessage[] {
  const input = { scopeId, maxChars: PAGE_STATE_CHARS };
  const value = typeof state === "string" ? state : JSON.stringify(state);
  return [
    { role: "assistant", content: [{ type: "tool-call", toolCallId: callId, toolName: snapshotTool, input }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId: callId, toolName: snapshotTool, output: { type: "text", value } }] },
  ];
}
