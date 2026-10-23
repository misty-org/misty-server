import type { ModelMessage, ToolResultPart } from "ai";

// The working set (docs/agents/harness.md, Context). A run's context grew to 35,000
// input tokens at the median and 100,000 at p90, because every page snapshot, extract and fetch
// stayed until the transcript neared the model's window. Each turn reread all of it: turns took
// 9.4 s and the model lost the thread. Like browser-use, the run keeps the current page state
// and short notes on the past, not every old page.
//
// Above WORKING_TOKENS, results older than the newest KEEP_RESULTS are cut down: page states
// (snapshots, trees) entirely, since the agent acts only on the current page, and reading
// results (fetch, extract, search, find) to a short digest, so facts found earlier survive.
// The cut lands in batches: each cut rewrites earlier messages, so the provider's cached prefix
// is lost for that turn. Cutting one result a turn made every turn past the budget uncached.
// A cut waits until it saves CUT_BATCH_CHARS, so most turns still reuse the cache.

export const WORKING_TOKENS = 24_000;
/** What a cut must save (about 10,000 tokens) to be worth one uncached turn. */
export const CUT_BATCH_CHARS = 40_000;
/** The newest page states stay whole: the agent acts only on the page in front of it. */
const KEEP_PAGES = 2;
/** The newest reading results stay whole; older ones become digests. */
const KEEP_READING = 4;
/** Any other result stays whole among the newest few. */
const KEEP_RESULTS = 6;
const DIGEST_CHARS = 700;

/** Tools whose results are reading: what they found is kept as a digest. */
const reading = new Set(["web_fetch", "web_search", "browser_extract", "browser_find", "log_work_read", "browser_run"]);
/** Tools whose results are a page's state (its tree): stale after the next action. */
const pageState = new Set(["browser_snapshot", "browser_navigate", "browser_steps", "browser_fill_form", "browser_skill", "browser_visual", "browser_inspect"]);

const CUT = "[Older result cut to keep this run fast; what it found should be in your notebook or answer.]";

export const DIGEST_MARK = "[Digest of an older result]";

function digest(text: string): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return `${DIGEST_MARK} ${flat.slice(0, DIGEST_CHARS)}${flat.length > DIGEST_CHARS ? " …" : ""}`;
}

function outputText(output: ToolResultPart["output"]): string | undefined {
  if (output.type === "text") return output.value;
  if (output.type === "json") return JSON.stringify(output.value);
  return undefined;
}

function cut(part: ToolResultPart): ToolResultPart | undefined {
  const text = outputText(part.output);
  if (text === undefined || text === CUT || text.startsWith(DIGEST_MARK)) return undefined;
  if (reading.has(part.toolName)) {
    if (text.length <= DIGEST_CHARS + DIGEST_MARK.length + 4) return undefined;
    return { ...part, output: { type: "text", value: digest(text) } };
  }
  if (text.length <= 400) return undefined;
  return { ...part, output: { type: "text", value: CUT } };
}

/** Cuts tool results past the newest few of each kind to a digest or a short note. */
export function cutOldResults(messages: ModelMessage[], keep = KEEP_RESULTS): { messages: ModelMessage[]; cut: number } {
  const seen = { page: 0, reading: 0, other: 0 };
  let count = 0;
  const out = [...messages];
  for (let index = out.length - 1; index >= 0; index--) {
    const message = out[index]!;
    if (message.role !== "tool") continue;
    const content = [...message.content].reverse().map((part) => {
      if (part.type !== "tool-result") return part;
      const kind = pageState.has(part.toolName) ? "page" : reading.has(part.toolName) ? "reading" : "other";
      seen[kind]++;
      const limit = kind === "page" ? KEEP_PAGES : kind === "reading" ? KEEP_READING : keep;
      if (seen[kind] <= limit) return part;
      const next = cut(part);
      if (!next) return part;
      count++;
      return next;
    });
    out[index] = { ...message, content: content.reverse() };
  }
  return { messages: out, cut: count };
}

/** A transcript's size by kind, and its largest parts, for logs. */
export function contextParts(messages: ModelMessage[]): { totalChars: number; byKind: Record<string, number>; largest: Array<[string, number]> } {
  const byKind: Record<string, number> = {};
  const parts: Array<[string, number]> = [];
  for (const message of messages) {
    const items = typeof message.content === "string" ? [{ type: "text", text: message.content }] : message.content;
    for (const part of items as Array<Record<string, unknown>>) {
      const size = JSON.stringify(part).length;
      const output = part.output as { type?: string; value?: unknown } | undefined;
      const label = `${message.role}:${String(part.type)}${part.toolName ? `:${String(part.toolName)}` : ""}${output?.type ? `:${output.type}` : ""}`;
      byKind[label] = (byKind[label] ?? 0) + size;
      parts.push([label, size]);
    }
  }
  return {
    totalChars: parts.reduce((sum, [, size]) => sum + size, 0),
    byKind,
    largest: parts.sort((a, b) => b[1] - a[1]).slice(0, 6),
  };
}
