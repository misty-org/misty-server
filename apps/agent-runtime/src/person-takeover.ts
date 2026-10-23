import type { ModelMessage } from "ai";

// Take over (docs/agents/cursor-companion.md, "Agent windows"): the person pauses the
// agent to use its window themselves. The run waits at its boundary, polling every
// TAKEOVER_POLL without model turns, for at most TAKEOVER_LIMIT polls; on resume its next turn
// starts from what the window shows now, not from snapshots taken before.

export const TAKEOVER_POLL = "10s";
export const TAKEOVER_LIMIT = 180;

export const resumeNote =
  "The person took over your window while you were paused and may have changed its pages, tabs or Space. " +
  "Before acting, read your tabs again (browser_snapshot) and check your earlier steps against what they show now; " +
  "do not repeat a step whose result you can't confirm.";

/** The resume note goes into the transcript once, after any messages the person sent. */
export function afterTakeover(messages: ModelMessage[], sent: string[]): ModelMessage[] {
  return [...messages, ...sent.map((text) => ({ role: "user" as const, content: text })), { role: "user", content: resumeNote }];
}
