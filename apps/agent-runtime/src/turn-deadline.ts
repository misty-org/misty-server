import type { ModelMessage } from "ai";

// The deliverable deadline (docs/plans/odysseys-campaign.md). A long Odysseys task reached
// the turn limit while still browsing and ended with no answer, losing every page it read.
// Two turns before the limit the run is told to write its answer; on the last turn only
// agent_finish is offered, so a run always reports what it found and what remains.

const marker = "Turn budget";

/** The note to add before this turn, once, or undefined. */
export function deadlineNote(turn: number, limit: number, messages: ModelMessage[]): string | undefined {
  const left = limit - turn;
  if (limit < 6 || left > 2) return undefined;
  const said = messages.some((message) => message.role === "user" && typeof message.content === "string" && message.content.startsWith(marker));
  if (said) return undefined;
  return `${marker}: this run has ${left} model turn${left === 1 ? "" : "s"} left. Stop gathering. Write your final answer now with agent_finish from everything you found: give each finding with its source, and list in remaining what is still undone and why.`;
}
