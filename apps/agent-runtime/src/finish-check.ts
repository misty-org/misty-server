import type { ModelMessage } from "ai";

// A "completed" report must not claim work that no tool did: a draft written
// only into the answer is not a Gmail draft. When the request asks for
// something to be made, or the report says it was, and no write succeeded in
// the run, the first completed agent_finish is refused once with the reason.

// What the person asked to have made or done somewhere other than the answer.
const requestedAction =
  /\b(draft|create|make|post|send|schedule|book|add|put|file|open an? (issue|pull request|pr)|save|label|invite|reply to)\b/i;
// The report saying, in the first person, that it made or changed something.
const claimedAction =
  /\bI(?:'ve| have)?\s+(created|drafted|saved|posted|sent|scheduled|booked|added|filed|opened|made|put|labeled|invited|replied)\b/i;
// The request naming where the thing belongs, so text in the answer can't stand in for it.
const namedPlace = /\b(gmail|e-?mail|inbox|slack|channel|github|issue|calendar|linkedin|notion|drive|jira|linear)\b/i;
// Composio actions that only read.
const readAction = /_(FETCH|LIST|GET|SEARCH|FIND|READ|QUERY|RETRIEVE|DOWNLOAD|CHECK|LOOKUP|DESCRIBE|COUNT)(_|$)/;
// Bookkeeping tools that change nothing the person asked for.
const bookkeeping = /^(log_work_|agent_|memory_)/;

type Part = { type?: string; toolCallId?: string; toolName?: string; input?: unknown; output?: unknown; text?: string };

function parts(message: ModelMessage): Part[] {
  return Array.isArray(message.content) ? (message.content as Part[]) : [];
}

function textOf(message: ModelMessage): string {
  if (typeof message.content === "string") return message.content;
  return parts(message).map((part) => (part.type === "text" ? part.text ?? "" : "")).join(" ");
}

function failed(output: unknown): boolean {
  if (!output || typeof output !== "object") return false;
  const value = output as { type?: string; value?: unknown };
  if (typeof value.type === "string" && value.type.startsWith("error")) return true;
  const body = value.value && typeof value.value === "object" ? (value.value as Record<string, unknown>) : {};
  return body.successful === false || typeof body.error === "string";
}

/** Whether a call changed something: a non-read Composio action, or a tool the catalog marks as a write. */
export function isWrite(toolName: string, input: unknown, readOnly: (name: string) => boolean): boolean {
  if (bookkeeping.test(toolName)) return false;
  if (toolName === "integration_execute") {
    const slug = String((input as { tool_slug?: unknown } | undefined)?.tool_slug ?? "").toUpperCase();
    return slug !== "" && !readAction.test(slug);
  }
  return !readOnly(toolName);
}

/** How many writes succeeded in the transcript so far. */
export function successfulWrites(messages: ModelMessage[], readOnly: (name: string) => boolean): number {
  const writes = new Map<string, boolean>();
  for (const message of messages) {
    for (const part of parts(message)) {
      if (part.type === "tool-call" && part.toolCallId && part.toolName && isWrite(part.toolName, part.input, readOnly)) writes.set(part.toolCallId, false);
      if (part.type === "tool-result" && part.toolCallId && writes.has(part.toolCallId)) writes.set(part.toolCallId, !failed(part.output));
    }
  }
  return [...writes.values()].filter(Boolean).length;
}

/** The person's own words in the run. */
function requestText(messages: ModelMessage[]): string {
  return messages.filter((message) => message.role === "user").map(textOf).join("\n");
}

/**
 * A second unbacked completed report, after the first was refused. When the request
 * named an app, the text in the answer can't be what was wanted, so the outcome becomes
 * partial; otherwise the agent decided the person only wanted the text, and it stands.
 */
export function afterRefusal<T extends { outcome: string; remaining: Array<{ requirement: string; reason: string }> }>(
  report: T,
  messages: ModelMessage[],
  readOnly: (name: string) => boolean,
): T {
  if (report.outcome !== "completed" || successfulWrites(messages, readOnly) > 0 || !namedPlace.test(requestText(messages))) return report;
  return {
    ...report,
    outcome: "partial",
    remaining: [...report.remaining, { requirement: "Make what was asked for in the app the request named", reason: "No tool call in this run created or changed it" }],
  };
}

/** The reason to refuse a completed report, or undefined when it may stand. */
export function unbackedCompletion(
  report: { outcome: string; summary: string },
  messages: ModelMessage[],
  readOnly: (name: string) => boolean,
): string | undefined {
  if (report.outcome !== "completed") return undefined;
  const request = requestText(messages);
  if (!requestedAction.test(request) && !claimedAction.test(report.summary)) return undefined;
  if (successfulWrites(messages, readOnly) > 0) return undefined;
  return "No tool call in this run created or changed anything, but the request asks for that or your summary says it happened. " +
    "Writing a draft, post, event or task into your answer does not make it. An email, event, post or task belongs in the person's connected app when one exists: " +
    "load connected apps with agent_tool_load (integration) and check integration_connected before deciding none is available. Call the tool that makes it, then finish; " +
    "or finish with outcome partial and say what is missing. If the person only wanted the text in your answer (they named no app), call agent_finish again with the same report.";
}
