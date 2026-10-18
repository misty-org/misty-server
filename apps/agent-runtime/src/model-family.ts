// A short note per model family (docs/plans/agent-harness.md, phase 5). Every
// family gets the same harness; these lines only correct habits a family
// showed in the workflow evals. Each line names the run it came from, so it
// can be dropped once the evals no longer need it.

type Family = "anthropic" | "openai" | "google" | "other";

export function modelFamily(modelId: string): Family {
  const provider = modelId.split("/")[0]?.toLowerCase() ?? "";
  if (provider === "anthropic") return "anthropic";
  if (provider === "openai") return "openai";
  if (provider === "google") return "google";
  return "other";
}

const notes: Record<Family, string> = {
  // Baseline 2026-10-16: gpt-6-luna made 39 calls on a three-vendor price
  // comparison and 124 searches for a one-page brief.
  openai:
    "Be economical with tools. Most requests need a handful of calls: search once or twice per question, read the pages that matter with web_fetch, then answer. Don't repeat a search with new wording when a page you found would answer it, and stop as soon as you can answer well.",
  // Baseline 2026-10-16: claude-haiku-5.5 named the group on screen but said it
  // couldn't identify the members instead of looking them up.
  anthropic:
    "When the person asks about something you can identify but need facts you don't have, look them up with your tools before saying you can't answer.",
  // No baseline runs yet; the shared harness rules apply.
  google: "Call tools with exactly the arguments their schemas describe, and end every task that used tools with agent_finish.",
  other: "",
};

export function modelFamilyNote(modelId: string): string {
  return notes[modelFamily(modelId)];
}
