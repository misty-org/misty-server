import type { ModelMessage } from "ai";
import { ledgerHeading } from "./requirements-review.js";

// Research budget (docs/agents/harness.md, Completion). Prompts say "be economical"; this layer makes
// it true. It reads the run's own transcript before every model call, so a replayed run agrees,
// and moves through three stages: open, wrap-up (the model is told what it has spent and must
// name the fact still missing) and closed (search and fetch leave the tool list, so the next
// turn can only answer). A repeat of an earlier query or page costs double, so a run that
// circles closes sooner than one that keeps finding new ground.

export const researchLimits = { wrapUp: 6, close: 12 } as const;

type ResearchStage = "open" | "wrap-up" | "closed";

interface ResearchUse {
  searches: number;
  fetches: number;
  repeats: number;
  /** Calls, with each repeat counted twice. */
  cost: number;
  stage: ResearchStage;
}

/** Model-visible names of the research tools this run has (either may be absent). */
export interface ResearchTools {
  search?: string;
  fetch?: string;
  /** screen_open, when the run can open its own browser tab: the route for a page a fetch can't read. */
  open?: string;
}

const stopWords = new Set(["the", "and", "for", "with", "what", "how", "are", "was", "who", "that", "this", "from", "about", "does", "did"]);

function queryWords(query: string): Set<string> {
  return new Set(query.toLowerCase().split(/[^a-z0-9]+/).filter((word) => word.length > 2 && !stopWords.has(word)));
}

/** The same question in other words: nearly every word of the shorter query is in the other. */
export function sameQuery(a: Set<string>, b: Set<string>): boolean {
  const smaller = Math.min(a.size, b.size);
  if (smaller < 2) return false;
  let shared = 0;
  for (const word of a) if (b.has(word)) shared++;
  return shared / smaller >= 0.8;
}

export function pageKey(url: string): string {
  try {
    const parsed = new URL(url);
    parsed.hash = "";
    return `${parsed.hostname.toLowerCase()}${parsed.pathname.replace(/\/+$/, "")}${parsed.search}`;
  } catch {
    return url.trim().toLowerCase();
  }
}

function stageFor(cost: number, scale = 1): ResearchStage {
  return cost >= researchLimits.close * scale ? "closed" : cost >= researchLimits.wrapUp * scale ? "wrap-up" : "open";
}

/**
 * A long request earns a longer budget: one base allowance for every step of the run's
 * own checklist (log_work_checklist) or every requirement listed from the request, whichever
 * is more, never less than one nor more than twenty. A quick question has no
 * checklist and keeps the base budget; a six-part task gets about 36 lookups before the
 * wrap-up note (docs/agents/evals/odysseys.md).
 */
export function budgetScale(messages: ModelMessage[]): number {
  const steps = new Set<string>();
  // The requirements Misty listed from a long request (src/requirements-review.ts) count too:
  // a run with 26 pages to verify outgrew an eight-step checklist's budget at a third of its turns.
  let requirements = 0;
  for (const message of messages) {
    if (message.role === "user" && typeof message.content === "string" && message.content.startsWith(ledgerHeading)) {
      requirements = Math.max(requirements, message.content.split("\n").filter((line) => /^\s*\d+[.)]\s/.test(line)).length);
    }
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type !== "tool-call" || part.toolName !== "log_work_checklist") continue;
      const items = ((part.input ?? {}) as { items?: Array<{ id?: unknown }> }).items ?? [];
      for (const item of items) if (typeof item?.id === "string") steps.add(item.id);
    }
  }
  return Math.min(20, Math.max(1, steps.size, requirements));
}

/**
 * What the run has spent on research so far, read from its tool calls. `minScale` is the largest
 * budget the run has had: a summarized transcript loses the checklist and requirements that set
 * it, and on October 20 a 200-turn run lost search and fetch at turn 29 that way.
 */
export function researchUse(messages: ModelMessage[], tools: ResearchTools, minScale = 1): ResearchUse {
  const use = { searches: 0, fetches: 0, repeats: 0 };
  const queries: Array<Set<string>> = [];
  const pages = new Set<string>();
  for (const message of messages) {
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type !== "tool-call") continue;
      const input = (part.input ?? {}) as { query?: unknown; url?: unknown };
      if (tools.search && part.toolName === tools.search) {
        use.searches++;
        const words = queryWords(String(input.query ?? ""));
        if (queries.some((earlier) => sameQuery(earlier, words))) use.repeats++;
        queries.push(words);
      } else if (tools.fetch && part.toolName === tools.fetch) {
        use.fetches++;
        const key = pageKey(String(input.url ?? ""));
        if (pages.has(key)) use.repeats++;
        pages.add(key);
      }
    }
  }
  const cost = use.searches + use.fetches + use.repeats;
  return { ...use, cost, stage: stageFor(cost, Math.max(minScale, budgetScale(messages))) };
}

/** The tools to hide: both research tools once the budget is closed. */
export function closedResearchTools(use: ResearchUse, tools: ResearchTools): Set<string> {
  if (use.stage !== "closed") return new Set();
  return new Set([tools.search, tools.fetch].filter((name): name is string => Boolean(name)));
}

const noteMarker = "Research budget";

/** The note for a stage, or undefined when it is open or the transcript already carries it. */
export function researchNote(use: ResearchUse, messages: ModelMessage[], tools: ResearchTools = {}): string | undefined {
  if (use.stage === "open") return undefined;
  const marker = `${noteMarker} (${use.stage})`;
  const said = messages.some((message) => {
    if (message.role !== "user") return false;
    const text = typeof message.content === "string" ? message.content : message.content.map((part) => (part.type === "text" ? part.text : "")).join(" ");
    return text.startsWith(marker);
  });
  if (said) return undefined;
  const spent = `${use.searches} search${use.searches === 1 ? "" : "es"} and ${use.fetches} page read${use.fetches === 1 ? "" : "s"}` +
    (use.repeats ? `, ${use.repeats} of them repeats` : "");
  // A run with its own tabs does its remaining work on pages, not in searches. "Answer now" made
  // such runs give up: a 20-school task ended "not finished" after 24 lookups (October 19).
  if (tools.open) {
    if (use.stage === "wrap-up")
      return `${marker}: this run has used ${spent}. Stop searching with new wording. Do the rest of the request on pages: open the sites you already know in your own tabs with ${tools.open}, use their own search boxes, menus and links (browser_steps), and read them with browser_snapshot or browser_extract, which are not counted here. Finish every part the request names before agent_finish.`;
    return `${marker}: this run has used ${spent}, so web search and fetch are closed. Keep working in your own tabs: ${tools.open} opens a site's address, and its own search, menus and links reach the rest. Finish every part the request names, then call agent_finish and say plainly what you could not confirm.`;
  }
  if (use.stage === "wrap-up")
    return `${marker}: this run has used ${spent}. Answer now from what you have unless one specific fact is still missing. If it is, name it and make the single call that finds it; do not search again with new wording.`;
  return `${marker}: this run has used ${spent}, so search and page reading are closed. Answer from what you found, say plainly what you could not confirm, and call agent_finish.`;
}
