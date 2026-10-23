import type { ModelMessage } from "ai";
import { pageKey, type ResearchTools } from "./research-budget.js";

// Citation grounding (docs/agents/harness.md, Completion). A search result is a lead, not a source:
// its snippet can be stale or cut off. A final answer may cite a page only if the run read it,
// with web_fetch or in a browser. The first agent_finish that cites an unread page is refused
// once with the list (a plain-text final answer is checked the same way), so the model reads those pages, drops the citation, or says the fact
// came from a search snippet. The second attempt stands, so this can never loop.

export const citationInstructions =
  "Cite only pages you read. A search result is a lead, not a source: read the page with web_fetch before citing it, or say the fact comes from a search snippet.";

type Part = { type?: string; toolCallId?: string; toolName?: string; input?: unknown; output?: unknown; text?: string };

const parts = (message: ModelMessage): Part[] => (Array.isArray(message.content) ? (message.content as Part[]) : []);

function failed(output: unknown): boolean {
  if (!output || typeof output !== "object") return false;
  const value = output as { type?: string; value?: unknown };
  if (typeof value.type === "string" && value.type.startsWith("error")) return true;
  const body = value.value && typeof value.value === "object" ? (value.value as Record<string, unknown>) : {};
  return body.successful === false || typeof body.error === "string";
}

const urlPattern = /https?:\/\/[^\s)\]>"'`]+|\b(?:[a-z0-9-]+\.)+[a-z]{2,}\/[^\s)\]>"'`]+/gi;

/** The pages an answer names, as page keys, without sentence punctuation. */
export function citedPages(text: string): Map<string, string> {
  const cited = new Map<string, string>();
  for (const match of text.matchAll(urlPattern)) {
    const url = match[0].replace(/[.,;:!?*_]+$/, "");
    const key = pageKey(/^https?:/i.test(url) ? url : `https://${url}`);
    if (!cited.has(key)) cited.set(key, url);
  }
  return cited;
}

/** Pages the run read: fetched successfully, opened in a browser, or handed over by the person. */
export function readPages(messages: ModelMessage[], tools: ResearchTools): Set<string> {
  const read = new Set<string>();
  const fetches = new Map<string, string>();
  for (const message of messages) {
    if (message.role === "user") {
      const text = typeof message.content === "string" ? message.content : parts(message).map((part) => (part.type === "text" ? part.text ?? "" : "")).join(" ");
      for (const key of citedPages(text).keys()) read.add(key);
      continue;
    }
    for (const part of parts(message)) {
      const url = (part.input as { url?: unknown } | undefined)?.url;
      if (part.type === "tool-call" && typeof url === "string" && part.toolName !== undefined && part.toolName !== tools.search) {
        const key = pageKey(url);
        if (part.toolName === tools.fetch && part.toolCallId) fetches.set(part.toolCallId, key);
        else read.add(key);
      }
      if (part.type === "tool-result" && part.toolCallId && fetches.has(part.toolCallId) && !failed(part.output)) read.add(fetches.get(part.toolCallId)!);
    }
  }
  return read;
}

/** Pages that only appeared in search results, never opened. */
function searchedPages(messages: ModelMessage[], tools: ResearchTools): Set<string> {
  const seen = new Set<string>();
  if (!tools.search) return seen;
  for (const message of messages)
    for (const part of parts(message))
      if (part.type === "tool-result" && part.toolName === tools.search)
        for (const key of citedPages(JSON.stringify(part.output ?? "")).keys()) seen.add(key);
  return seen;
}

/**
 * Cited pages the run saw only as search results and never read, as the answer wrote them.
 * A link an app tool returned (an issue it opened) or one the person gave is not in question.
 */
export function unreadCitations(answer: string, messages: ModelMessage[], tools: ResearchTools): string[] {
  const read = readPages(messages, tools);
  const searched = searchedPages(messages, tools);
  return [...citedPages(answer)].filter(([key]) => searched.has(key) && !read.has(key)).map(([, url]) => url);
}

/** The reason to refuse a report once, in words the model can act on. */
export function citationRefusal(unread: string[], canFetch: boolean, then = "call agent_finish again"): string {
  const list = unread.slice(0, 5).join(", ");
  const fix = canFetch
    ? "Read each with web_fetch (pass the question you need answered) and cite it from what it says; or remove the citation; or say the fact comes from a search snippet."
    : "Page reading is closed for this run, so remove those citations, or say the fact comes from a search snippet; do not present it as read.";
  return `Your answer cites pages this run never read: ${list}. A search result is only a lead. ${fix} Then ${then}.`;
}
