/** Only the control plane's explicit pre-dispatch rejection permits replanning.
 * An uncertain result, denial, transport error, or arbitrary tool failure does not. */
export function requiresBrowserReinspection(name: string, output: unknown): boolean {
  if (!["browser.upload", "browser.steps", "browser.fill_form"].includes(name)) return false;
  if (!output || typeof output !== "object") return false;
  const result = output as Record<string, unknown>;
  return result.status === "failure" && result.reason === "browser_snapshot_stale" && result.attempted === false;
}

/** Snapshot refs and inspections carry different references. */
export function browserReinspectionTool(name: string): string {
  if (name === "browser.steps" || name === "browser.fill_form") return "browser.snapshot";
  return "browser.inspect";
}

/** Told to the model with its visible tool names before the next turn. */
export function browserReinspectionInstruction(name: string): string {
  if (name === "browser.snapshot")
    return "The previous page action was rejected before dispatch because the page changed since its snapshot. Call browser_snapshot now, then act with the new refs and documentId. Do not report that the rejected action succeeded.";
  return "The previous browser action was rejected before dispatch because its page inspection was stale. Call browser_inspect now, then decide the next action using fresh references. Do not report that the rejected action succeeded.";
}
