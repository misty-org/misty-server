/** Only the control plane's explicit pre-dispatch rejection permits replanning.
 * An uncertain result, denial, transport error, or arbitrary tool failure does not. */
export function requiresBrowserReinspection(name: string, output: unknown): boolean {
  if (!["browser.click", "browser.interact", "browser.type", "browser.upload"].includes(name)) return false;
  if (!output || typeof output !== "object") return false;
  const result = output as Record<string, unknown>;
  return result.status === "failure" && result.reason === "browser_snapshot_stale" && result.attempted === false;
}
