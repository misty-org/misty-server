import type { ModelMessage } from "ai";

// Proof tabs (docs/agents/harness.md, Completion). Odysseys asks for pages "kept open"
// as proof in most of its tasks, and those items failed most often: the agent read the page with
// a fetch, closed the tab, or left it on a generic page or behind a dialog. When the request asks
// for pages left open, the first finish goes back once, unless the agent has just listed its
// tabs, to check each one against the request.

const keptOpen =
  /\b(keep|kept|leave|left|stay|remain)\b[^.]{0,80}\b(open|tabs?)\b|\b(in|on) (its|their|a|separate|different) (own )?tabs?\b|\bvisual proof\b|\bopen (in|as) (separate|different|new) tabs?\b/i;

/** Whether the person's request asks for pages left open. */
export function asksForOpenPages(request: string): boolean {
  return keptOpen.test(request);
}

/** Whether one of the last `within` tool calls was `tool`. */
function calledRecently(messages: ModelMessage[], tool: string, within = 4): boolean {
  let seen = 0;
  for (let index = messages.length - 1; index >= 0 && seen < within; index--) {
    const message = messages[index]!;
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of [...message.content].reverse()) {
      if (part.type !== "tool-call") continue;
      if (part.toolName === tool) return true;
      if (++seen >= within) break;
    }
  }
  return false;
}

/** The reason to send a finish back to check its tabs, or undefined. */
export function proofTabsRefusal(request: string, messages: ModelMessage[], tabList: string | undefined, program?: string): string | undefined {
  if (!asksForOpenPages(request) || (!program && (!tabList || calledRecently(messages, tabList)))) return undefined;
  // A run with programs checks its own tabs with one (the person's tab list is not its to use),
  // and brings what it reports into view: each kept tab is pictured as it stands at the end.
  const how = program
    ? `call ${program} with \`return listTabs()\` (every tab's address and title) and, for each page the request names, \`await t.scrollTo("the text that shows what you report")\` so the evidence is on screen, then \`await t.keep()\` so the tab stays awake and is pictured when you finish (a tab you do not keep() sleeps)`
    : `call ${tabList}`;
  return (
    `The person asked for pages to be left open, and those tabs are part of the answer. Before finishing, ${how}, ` +
    "and check each page the request names against your tabs: it is open in its own tab, on the right page (the item, not a search " +
    "or home page), in the state asked for (the search run, the option or size chosen, scrolled to the evidence) and not covered by " +
    "a dialog or banner. Open or fix any that are missing, then call agent_finish again."
  );
}

/** A page title or address that says the page is an error or empty, not the evidence asked for. */
const errorPage = /\b404\b|page not found|not found|can.?t (find|locate)|no longer (exists|available)|access denied|forbidden|just a moment|^about:blank$/i;

/**
 * Kept tabs (browser_run's keep()) that end on an error or empty page, from the run's latest
 * program results (docs/agents/harness.md, Completion): the proof the person asked to see
 * must show the page, not "We Can't Find That Page".
 */
export function brokenProofTabs(messages: ModelMessage[]): string[] {
  const kept = new Set<string>();
  const latest = new Map<string, { url: string; title: string }>();
  for (const message of messages) {
    if (message.role !== "tool" || !Array.isArray(message.content)) continue;
    for (const part of message.content) {
      if (part.type !== "tool-result" || part.toolName !== "browser_run") continue;
      const output = part.output as { type?: string; value?: unknown };
      const value = (output.type === "json" ? output.value : typeof output.value === "string" ? safeParse(output.value) : undefined) as
        | { kept?: unknown; tabs?: unknown }
        | undefined;
      for (const id of Array.isArray(value?.kept) ? value.kept : []) if (typeof id === "string") kept.add(id);
      for (const tab of Array.isArray(value?.tabs) ? value.tabs : []) {
        const { id, url, title } = (tab ?? {}) as { id?: unknown; url?: unknown; title?: unknown };
        if (typeof id === "string") latest.set(id, { url: String(url ?? ""), title: String(title ?? "") });
      }
    }
  }
  return [...kept].flatMap((id) => {
    const page = latest.get(id);
    return page && (errorPage.test(page.title) || errorPage.test(page.url)) ? [`${page.url || id} ("${page.title}")`] : [];
  });
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}
