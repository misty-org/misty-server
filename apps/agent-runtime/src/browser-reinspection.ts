/** Only the control plane's explicit pre-dispatch rejection permits replanning.
 * An uncertain result, denial, transport error, or arbitrary tool failure does not. */
export function requiresBrowserReinspection(name: string, output: unknown): boolean {
  if (!["browser.click", "browser.interact", "browser.type", "browser.upload", "browser.workspace.interact"].includes(name)) return false;
  if (!output || typeof output !== "object") return false;
  const result = output as Record<string, unknown>;
  return result.status === "failure" && result.reason === "browser_snapshot_stale" && result.attempted === false;
}

/** Workspace screenshots and website inspections carry different references. */
export function browserReinspectionTool(name: string): string {
  return name.startsWith("browser.workspace.") ? "browser.workspace.visual" : "browser.inspect";
}

/** Told to the model with its visible tool names before the next turn. */
export function browserReinspectionInstruction(name: string): string {
  return name === "browser.workspace.visual"
    ? "The previous screen action consumed its screenshot or was rejected before dispatch. Call browser_workspace_visual now, verify any dispatched action, then decide the next action using the new documentId. Never reuse a consumed screenshot or claim that rejected input succeeded."
    : "The previous browser action was rejected before dispatch because its page inspection was stale. Call browser_inspect now, then decide the next action using fresh references. Do not report that the rejected action succeeded.";
}
