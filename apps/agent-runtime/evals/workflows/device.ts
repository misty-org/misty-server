// The person's own computer as a run's device (trusted_devices.id), signed through the
// debug build's eval bridge: runs carry the workspace context a chat sent from it carries,
// so device tools (web.fetch, the agent's own browser session) run on it as they would for
// a real request. Shared by the workflow and Odysseys evals.

export const deviceId = process.env.MISTY_WORKFLOW_EVAL_DEVICE?.trim() || "";
export const device = /^device_[0-9a-f-]{36}$/.test(deviceId);
const workspaceCapabilities = ["bookmark.add", "bookmark.list", "browser.run", "browser.session", "browser.tab.list", "browser.tab.open", "web.fetch"];

/** The workspace context for one run, with a fresh grant the desktop signs. */
export async function deviceContexts(userId: string, agentId: string): Promise<unknown[]> {
  if (!device) return [];
  const bridge = process.env.MISTY_BROWSER_EVAL_BRIDGE?.replace(/\/$/, "");
  const token = process.env.MISTY_BROWSER_EVAL_BRIDGE_TOKEN;
  if (!bridge || !token) throw new Error("A device needs the desktop's eval bridge: set MISTY_BROWSER_EVAL_BRIDGE and its token.");
  const scope = `workspace:${deviceId}`;
  const response = await fetch(`${bridge}/run-grants`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({
      // The API base the desktop signed in with (its session is kept per origin), which
      // can differ from the address this runner uses for the same server.
      apiBase: process.env.MISTY_WORKFLOW_EVAL_DEVICE_API_BASE || `${process.env.MISTY_ACCEPTANCE_API ?? "http://127.0.0.1:8081"}/v1`, accountId: userId,
      grants: [{ targetDeviceId: deviceId, agentId, capabilities: workspaceCapabilities, scopes: [scope] }],
    }),
  });
  const signed = (await response.json()) as { ok: boolean; result?: unknown[]; error?: string };
  if (!signed.ok || !signed.result?.[0]) throw new Error(`run grant: ${signed.error ?? response.status}`);
  return [{ device_id: deviceId, kind: "workspace", opaque_ref: scope, display_name: "Misty browser",
    capabilities: workspaceCapabilities, metadata: { source: "desktop" }, run_grant: signed.result[0] }];
}
