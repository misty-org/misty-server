// A screen that joins a run mid-run (docs/agents/evals/odysseys.md): screen_open
// with no place named opens the agent's own browser tab as a device job and
// attaches it to this run, reporting catalog_changed. The loop then reads Misty's
// catalog again so the run's browser tools reach the model in the same run.

/** Whether any tool result this turn says the run's catalog grew. */
export function screenJoined(results: ReadonlyArray<{ output?: unknown }> | undefined): boolean {
  return (results ?? []).some((result) => catalogChanged(result.output));
}

function catalogChanged(output: unknown, depth = 0): boolean {
  if (depth > 6 || output === null || output === undefined) return false;
  if (typeof output === "string") {
    if (!output.includes("catalog_changed")) return false;
    try {
      return catalogChanged(JSON.parse(output), depth + 1);
    } catch {
      return false;
    }
  }
  if (typeof output !== "object") return false;
  const record = output as Record<string, unknown>;
  // A screen reports status open; a browser_run program that opened tabs reports no status.
  if (record.catalog_changed === true && (record.status ?? "open") === "open") return true;
  return Object.values(record).some((value) => typeof value === "object" || typeof value === "string" ? catalogChanged(value, depth + 1) : false);
}
