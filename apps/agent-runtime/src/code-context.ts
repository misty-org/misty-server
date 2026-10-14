import type { ModelMessage, ToolResultPart } from "ai";

// Keeps code-project runs cheap (docs/plans/agent-code-projects.md, Cost).
//
// File reads and command output are most of a coding run's context, and most
// of it goes stale: a file read before it was edited, or read again later, no
// longer shows the file; old build logs only matter for their exit code. Like
// page snapshots (prompt-cache.ts), stale results are dropped in batches, so
// each drop costs one uncached turn instead of one per step.

/** Readers whose results show a file's content at one moment. */
const readTools = new Set(["code_read"]);
/** Writers that make earlier reads of the same path stale. */
const writeTools = new Set(["code_edit", "code_write"]);
/** Command output: only the newest few logs stay. */
const commandTools = new Set(["code_run", "code_logs"]);
const KEEP_COMMANDS = 3;
/** Stale text that triggers one batched drop. */
const STALE_BYTES = 48_000;

export const DROPPED_READ =
  "[An older read of this file was removed because the file was read again or changed since. Read it again if you need it.]";
export const DROPPED_OUTPUT = "[Older command output removed to save context. Run it again if you need the details.]";

function parsed(output: ToolResultPart["output"]): Record<string, unknown> | null {
  let value: unknown;
  if (output.type === "json") value = output.value;
  else if (output.type === "text") {
    try {
      value = JSON.parse(output.value);
    } catch {
      return null;
    }
  } else return null;
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function replaced(output: ToolResultPart["output"], value: Record<string, unknown>): ToolResultPart["output"] {
  return output.type === "json" ? { type: "json", value: value as never } : { type: "text", value: JSON.stringify(value) };
}

/** Each tool call's name and path, by call id. */
function callsById(messages: ModelMessage[]): Map<string, { tool: string; path: string }> {
  const calls = new Map<string, { tool: string; path: string }>();
  for (const message of messages) {
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type !== "tool-call") continue;
      const input = part.input as { path?: unknown; project?: unknown } | undefined;
      const path = typeof input?.path === "string" ? `${String(input.project ?? "")}\u0000${input.path.replace(/^\.?\/+/, "")}` : "";
      calls.set(part.toolCallId, { tool: part.toolName, path });
    }
  }
  return calls;
}

/**
 * Drops file reads that a later read or edit of the same file superseded,
 * and command output older than the newest few, once enough is stale.
 */
export function pruneStaleCodeResults(messages: ModelMessage[]): { messages: ModelMessage[]; dropped: number } {
  const calls = callsById(messages);
  type Located = { message: number; part: number; bytes: number; kind: "read" | "output" };
  const results: Array<Located & { tool: string; path: string }> = [];
  messages.forEach((message, messageIndex) => {
    if (message.role !== "tool") return;
    message.content.forEach((part, partIndex) => {
      if (part.type !== "tool-result") return;
      const call = calls.get(part.toolCallId) ?? { tool: part.toolName, path: "" };
      const value = parsed(part.output);
      if (!value) return;
      const text = typeof value.content === "string" ? value.content : typeof value.output === "string" ? value.output : typeof value.logs === "string" ? value.logs : "";
      if (text === DROPPED_READ || text === DROPPED_OUTPUT) return;
      const kind = readTools.has(part.toolName) ? "read" : commandTools.has(part.toolName) ? "output" : null;
      results.push({ message: messageIndex, part: partIndex, bytes: text.length, kind: kind ?? "read", tool: part.toolName, path: call.path });
    });
  });
  const stale: Located[] = [];
  results.forEach((result, index) => {
    if (readTools.has(result.tool) && result.path) {
      const later = results.slice(index + 1).some((next) => next.path === result.path && (readTools.has(next.tool) || writeTools.has(next.tool)));
      if (later && result.bytes) stale.push(result);
    }
  });
  const outputs = results.filter((result) => commandTools.has(result.tool) && result.bytes);
  stale.push(...outputs.slice(0, Math.max(0, outputs.length - KEEP_COMMANDS)));
  if (stale.reduce((sum, item) => sum + item.bytes, 0) < STALE_BYTES) return { messages, dropped: 0 };
  const out = [...messages];
  let dropped = 0;
  for (const { message, part, kind } of stale) {
    const current = out[message]!;
    if (current.role !== "tool") continue;
    const content = [...current.content];
    const result = content[part] as ToolResultPart;
    const value = parsed(result.output);
    if (!value) continue;
    const next =
      kind === "read"
        ? { path: value.path, totalLines: value.totalLines, content: DROPPED_READ }
        : { exitCode: value.exitCode, running: value.running, timedOut: value.timedOut, output: DROPPED_OUTPUT };
    content[part] = { ...result, output: replaced(result.output, next) };
    out[message] = { ...current, content };
    dropped++;
  }
  return { messages: out, dropped };
}
