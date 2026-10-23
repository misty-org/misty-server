// A short note for the house model's family (docs/agents/harness.md, Tool surface).
// Every model gets the same harness; this line only corrects a habit the family
// showed in the evals. It names the run it came from, so it can be dropped once
// the evals no longer need it.

// Baseline 2026-10-16: claude-haiku-5.5 named the group on screen but said it
// couldn't identify the members instead of looking them up.
const anthropicNote =
  "When the person asks about something you can identify but need facts you don't have, look them up with your tools before saying you can't answer.";

export function modelFamilyNote(modelId: string): string {
  return modelId.split("/")[0]?.toLowerCase() === "anthropic" ? anthropicNote : "";
}
