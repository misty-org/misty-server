import type { ModelMessage } from "ai";

// The person's time (docs/plans/odysseys-campaign.md). A run that is right but slow still fails
// the person waiting on it, and how slow is too slow depends on the task: a quick lookup should
// take a few minutes, deep research much longer. The agent says how long the task should take
// with its first checklist (`minutes`); the budget follows that estimate, ten minutes when it
// gave none. The run is told where it stands at 80%, asked to wrap up at 100%, and past about
// 130% can only write its answer. A run whose recent steps still find new things is not cut
// off by the clock (an unfinished task helps nobody): it keeps going until it stops progressing.

export const DEFAULT_ESTIMATE_MINUTES = 10;
const MIN_ESTIMATE_MINUTES = 2;
const MAX_ESTIMATE_MINUTES = 120;
/** Without a request for long work, an estimate counts for at most this many minutes. */
const ORDINARY_MAX_MINUTES = 30;
const longWork =
  /\b(deep|in-depth|thorough|comprehensive|exhaustive) (research|dive|analysis|review)|\b(write|draft|produce) (me )?(a |an |the )?(full |complete |long |detailed )?(paper|report|essay|thesis|white ?paper)|\bovernight\b|\btake (your|as much) time\b/i;

/** Whether the person's request asks for long work, which may take more than half an hour. */
export function asksForLongWork(messages: ModelMessage[]): boolean {
  const first = messages.find((message) => message.role === "user");
  const text =
    typeof first?.content === "string"
      ? first.content
      : (first?.content ?? []).map((part) => ("text" in part ? String(part.text) : "")).join(" ");
  return longWork.test(text);
}

const marker = "Time budget";
const checklistTool = "log_work_checklist";

const said = (messages: ModelMessage[], stage: string) =>
  messages.some(
    (message) =>
      message.role === "user" && typeof message.content === "string" && message.content.startsWith(`${marker} (${stage})`),
  );

/** The agent's latest estimate of the task's minutes, from its checklist calls, if any. */
export function estimateMinutes(messages: ModelMessage[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]!;
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of [...message.content].reverse()) {
      if (part.type !== "tool-call" || part.toolName !== checklistTool) continue;
      const minutes = Number((part.input as { minutes?: unknown } | undefined)?.minutes);
      if (Number.isFinite(minutes) && minutes > 0) return minutes;
    }
  }
  return undefined;
}

/** A short, stable fingerprint of a tool result, without the ids and refs that change on every read. */
function fingerprint(output: unknown): string {
  const text = (typeof output === "string" ? output : JSON.stringify(output) ?? "")
    .replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, "")
    .replace(/\bref=e\d+\b|\[ref=e\d+\]/g, "");
  let hash = 2166136261;
  for (let index = 0; index < text.length; index++) hash = Math.imul(hash ^ text.charCodeAt(index), 16777619);
  return `${text.length}:${hash >>> 0}`;
}

/** What every tool result so far said, to tell later results that are new from repeats. */
export function resultPrints(messages: ModelMessage[]): Set<string> {
  return new Set(messages.flatMap((message) =>
    message.role === "tool" && Array.isArray(message.content)
      ? message.content.flatMap((part) => (part.type === "tool-result" ? [fingerprint(part.output)] : []))
      : [],
  ));
}

/**
 * Whether the run is still finding things: at least `needed` of its last `window` tool results
 * say something no earlier result did (a new page, new data). Repeats are not progress.
 */
export function stillProgressing(messages: ModelMessage[], window = 8, needed = 2): boolean {
  const results = messages.flatMap((message) =>
    message.role === "tool" && Array.isArray(message.content)
      ? message.content.flatMap((part) => (part.type === "tool-result" ? [fingerprint(part.output)] : []))
      : [],
  );
  const earlier = new Set(results.slice(0, -window));
  const recent = results.slice(-window);
  return recent.length >= needed && new Set(recent.filter((result) => !earlier.has(result))).size >= needed;
}

/**
 * Where the run stands against the person's time before this turn: the note to add, once per
 * stage, and whether only the final answer may be written now. `estimate` is the agent's own,
 * in minutes; `progressing` says the run's recent steps still find new things.
 */
export function timeBudget(
  elapsedMs: number,
  messages: ModelMessage[],
  estimate = DEFAULT_ESTIMATE_MINUTES,
  progressing = false,
): { note?: string; answerOnly: boolean } {
  // Agents overestimate: a request that doesn't ask for long work is held to half an hour.
  const ceiling = asksForLongWork(messages) ? MAX_ESTIMATE_MINUTES : ORDINARY_MAX_MINUTES;
  const budget = Math.min(ceiling, Math.max(MIN_ESTIMATE_MINUTES, estimate)) * 60_000;
  const answerAt = Math.max(budget * 1.3, budget + 90_000);
  const minutes = Math.floor(elapsedMs / 60_000);
  const expected = Math.round(budget / 60_000);
  // Past its estimate, a run still finding things keeps going; the clock alone ends nothing.
  if (elapsedMs >= budget && progressing)
    return {
      answerOnly: false,
      note: said(messages, "progress")
        ? undefined
        : `${marker} (progress): ${minutes} minutes in, past the ${expected} this task was expected to take. Your recent steps are still finding new things, so keep working through what is open, the most important parts first. Once your steps stop turning up anything new, write your answer.`,
    };
  if (elapsedMs >= answerAt) {
    return {
      answerOnly: true,
      note: said(messages, "answer")
        ? undefined
        : `${marker} (answer): this run has worked ${minutes} minutes against the ${expected} it should take, and the person is waiting. Write your final answer now with agent_finish from everything you found: give each finding with its source, and list in remaining what is still undone and why.`,
    };
  }
  if (elapsedMs >= budget && !said(messages, "wrap"))
    return {
      answerOnly: false,
      note: `${marker} (wrap): ${minutes} minutes in, the ${expected} this task should take. Finish only what is nearly done, open nothing new, and write your answer within the next few turns.`,
    };
  if (elapsedMs >= budget * 0.8 && !said(messages, "pace"))
    return {
      answerOnly: false,
      note: `${marker} (pace): ${minutes} of about ${expected} minutes used. Finish the parts in hand before starting others, and drop leads that are not paying off.`,
    };
  return { answerOnly: false };
}
