import type { ModelMessage, ToolResultPart } from "ai";

// Keeps long browser runs cheap and fast (agentic-browser plan, Phase 2).
//
// Providers cache the unchanged start of a request. The transcript only grows
// at its end, so every turn reuses the turn before it, as long as older
// messages stay byte-identical. Page snapshots are the bulk of a browser run
// and go stale after the next action, so older ones are dropped, but in
// batches: each drop rewrites earlier messages and costs one uncached turn.

/** Page tools whose results carry a snapshot tree. */
const snapshotTools = new Set(["browser_snapshot", "browser_steps", "browser_fill_form", "browser_navigate"]);
/** The newest snapshots stay; the model acts on them. */
const KEEP_SNAPSHOTS = 2;
/** Stale snapshot text that triggers one batched drop. */
const STALE_BYTES = 48_000;

export const DROPPED_SNAPSHOT = "[Older page snapshot removed; its refs may be stale. Take a new snapshot to see the page now.]";

type Rewrite = (value: Record<string, unknown>) => Record<string, unknown> | undefined;

/** Applies `rewrite` to a tool result's JSON text, or returns undefined to keep it. */
function rewriteOutput(output: ToolResultPart["output"], rewrite: Rewrite): ToolResultPart["output"] | undefined {
  if (output.type !== "text") return undefined;
  let value: unknown;
  try {
    value = JSON.parse(output.value);
  } catch {
    return undefined;
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const next = rewrite(value as Record<string, unknown>);
  return next ? { type: "text", value: JSON.stringify(next) } : undefined;
}

function snapshotText(value: Record<string, unknown>): string {
  if (typeof value.tree === "string") return value.tree;
  const nested = value.snapshot as Record<string, unknown> | undefined;
  return typeof nested?.tree === "string" ? nested.tree : "";
}

function dropSnapshot(value: Record<string, unknown>): Record<string, unknown> | undefined {
  if (typeof value.tree === "string" && value.tree !== DROPPED_SNAPSHOT) return { ...value, tree: DROPPED_SNAPSHOT };
  const nested = value.snapshot as Record<string, unknown> | undefined;
  if (typeof nested?.tree === "string" && nested.tree !== DROPPED_SNAPSHOT) return { ...value, snapshot: { ...nested, tree: DROPPED_SNAPSHOT } };
  return undefined;
}

/**
 * Replaces every page snapshot but the newest two with a short note, once the
 * stale ones add up to enough to be worth a cache miss.
 */
export function pruneStalePageSnapshots(messages: ModelMessage[]): { messages: ModelMessage[]; dropped: number } {
  const located: Array<{ message: number; part: number; bytes: number }> = [];
  messages.forEach((message, messageIndex) => {
    if (message.role !== "tool") return;
    message.content.forEach((part, partIndex) => {
      if (part.type !== "tool-result" || !snapshotTools.has(part.toolName)) return;
      let bytes = 0;
      rewriteOutput(part.output, (value) => {
        bytes = snapshotText(value) === DROPPED_SNAPSHOT ? 0 : snapshotText(value).length;
        return undefined;
      });
      if (bytes) located.push({ message: messageIndex, part: partIndex, bytes });
    });
  });
  const stale = located.slice(0, Math.max(0, located.length - KEEP_SNAPSHOTS));
  if (stale.reduce((sum, item) => sum + item.bytes, 0) < STALE_BYTES) return { messages, dropped: 0 };
  const out = [...messages];
  let dropped = 0;
  for (const { message, part } of stale) {
    const current = out[message]!;
    if (current.role !== "tool") continue;
    const content = [...current.content];
    const result = content[part] as ToolResultPart;
    const output = rewriteOutput(result.output, dropSnapshot);
    if (!output) continue;
    content[part] = { ...result, output };
    out[message] = { ...current, content };
    dropped++;
  }
  return { messages: out, dropped };
}

/**
 * Marks the newest message as an Anthropic cache breakpoint and clears older
 * marks; Anthropic allows four and caches everything before the newest one.
 * Other providers cache unchanged prefixes on their own.
 */
export function withCacheBreakpoint(messages: ModelMessage[], modelId: string): ModelMessage[] {
  if (!modelId.startsWith("anthropic/") || !messages.length) return messages;
  return messages.map((message, index) => {
    const options = { ...(message.providerOptions ?? {}) } as Record<string, Record<string, unknown>>;
    const { cacheControl: _previous, ...anthropic } = options.anthropic ?? {};
    if (index === messages.length - 1) anthropic.cacheControl = { type: "ephemeral" };
    if (Object.keys(anthropic).length) options.anthropic = anthropic;
    else delete options.anthropic;
    const { providerOptions: _old, ...rest } = message;
    return (Object.keys(options).length ? { ...rest, providerOptions: options } : rest) as ModelMessage;
  });
}

/** Where the server's run-specific part of the system prompt (time, tab, device) begins. */
const volatileMarkers = ["\n\nAuthoritative run context:", "\n\nCurrent time:"];

/**
 * Adds the runtime's fixed instructions to the server's system prompt ahead of the part that
 * changes every run. Providers cache a prompt only up to its first difference, so fixed text
 * after a timestamp would be re-read at full price by every new run.
 */
export function withRuntimeInstructions(system: string, fixed: string[]): string {
  const marker = volatileMarkers.map((value) => system.indexOf(value)).filter((index) => index >= 0).sort((a, b) => a - b)[0];
  const added = ["", ...fixed].join("\n");
  if (marker === undefined) return system + "\n" + added;
  return system.slice(0, marker) + added + system.slice(marker);
}
