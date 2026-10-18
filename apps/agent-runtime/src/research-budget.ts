import type { ModelMessage } from "ai";

// Research budget (docs/plans/agent-harness.md). Prompts say "be economical"; this layer makes
// it true. It reads the run's own transcript before every model call, so a replayed run agrees,
// and moves through three stages: open, wrap-up (the model is told what it has spent and must
// name the fact still missing) and closed (search and fetch leave the tool list, so the next
// turn can only answer). A repeat of an earlier query or page costs double, so a run that
// circles closes sooner than one that keeps finding new ground.

export const researchLimits = { wrapUp: 6, close: 12 } as const;

export type ResearchStage = "open" | "wrap-up" | "closed";

export interface ResearchUse {
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
 * A long request earns a longer budget: one base allowance for every two steps of the
 * run's own checklist (log_work_checklist), never less than one. A quick question has no
 * checklist and keeps the base budget (docs/plans/odysseys-campaign.md).
 */
export function budgetScale(messages: ModelMessage[]): number {
  const steps = new Set<string>();
  for (const message of messages) {
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type !== "tool-call" || part.toolName !== "log_work_checklist") continue;
      const items = ((part.input ?? {}) as { items?: Array<{ id?: unknown }> }).items ?? [];
      for (const item of items) if (typeof item?.id === "string") steps.add(item.id);
    }
  }
  return Math.max(1, Math.ceil(steps.size / 2));
}

/** What the run has spent on research so far, read from its tool calls. */
export function researchUse(messages: ModelMessage[], tools: ResearchTools): ResearchUse {
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
  return { ...use, cost, stage: stageFor(cost, budgetScale(messages)) };
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
  // A fact a page holds behind a toggle, a click or JavaScript is found by opening the page, not by searching.
  const browse = tools.open
    ? ` Facts on pages a fetch could not show (behind a toggle, a click, JavaScript or a sign-in) are not found by searching: open them in your own tab with ${tools.open} and the first page's url, then go to each other page that needs it with browser_navigate. Browser tools are not counted here, so finish every page the request needs.`
    : "";
  if (use.stage === "wrap-up")
    return `${marker}: this run has used ${spent}. Answer now from what you have unless one specific fact is still missing. If it is, name it and make the single call that finds it; do not search again with new wording.${browse}`;
  return `${marker}: this run has used ${spent}, so search and page reading are closed. Answer from what you found${tools.open ? ` or from pages you open with ${tools.open}` : ""}, say plainly what you could not confirm, and call agent_finish.`;
}
