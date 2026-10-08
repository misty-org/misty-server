// The benchmark's browser: a tab in Misty's own browser, in the system webview
// (WKWebView on macOS, WebView2 on Windows), reached through the desktop's
// debug bridge (src-tauri/src/infra/browser_eval_bridge.rs). Every tool runs
// through the same agent path production uses on the device.

export interface EvalBrowser {
  /** Runs one browser tool on the device, as a production device job would. */
  execute(operation: string, input?: Record<string, unknown>): Promise<Record<string, any>>;
  /** Reads the page's own state for a task's check; never shown to the model. */
  evaluate<T = unknown>(expression: string): Promise<T>;
  /** The tab as an image, for the judge. */
  screenshot(): Promise<{ data: string; mediaType: string }>;
  close(): Promise<void>;
}

export interface BridgeConfig {
  url: string;
  token: string;
}

/** The running desktop's bridge, from MISTY_BROWSER_EVAL_BRIDGE and _TOKEN. */
export function bridgeConfig(): BridgeConfig | undefined {
  const url = process.env.MISTY_BROWSER_EVAL_BRIDGE?.trim();
  const token = process.env.MISTY_BROWSER_EVAL_BRIDGE_TOKEN?.trim();
  if (!url || !token) return undefined;
  const parsed = new URL(url);
  if (parsed.protocol !== "http:" || !["127.0.0.1", "localhost"].includes(parsed.hostname))
    throw new Error("MISTY_BROWSER_EVAL_BRIDGE must be the desktop's loopback address, such as http://127.0.0.1:47123.");
  return { url: parsed.origin, token };
}

async function call<T>(bridge: BridgeConfig, path: string, body: unknown): Promise<T> {
  const response = await fetch(bridge.url + path, {
    method: "POST",
    headers: { authorization: `Bearer ${bridge.token}`, "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(`The desktop bridge refused ${path}: ${response.status} ${await response.text()}`);
  const reply = (await response.json()) as { ok: boolean; result?: T; error?: string };
  if (!reply.ok) throw new Error(reply.error || `The desktop bridge could not run ${path}.`);
  return reply.result as T;
}

/** Opens a Misty tab at url for one task. */
export async function openEvalTab(url: string, bridge = bridgeConfig()): Promise<EvalBrowser> {
  if (!bridge) throw new Error("Start the Misty desktop app with the browser eval bridge; see evals/browser/README.md.");
  const visible = process.env.MISTY_BROWSER_EVAL_VISIBLE === "1";
  const { scopeId } = await call<{ scopeId: string }>(bridge, "/tab", { url, visible });
  const execute = (operation: string, input: Record<string, unknown> = {}) =>
    call<Record<string, any>>(bridge, "/execute", { scopeId, operation, input });
  return {
    execute,
    evaluate: <T>(expression: string) => call<T>(bridge, "/evaluate", { scopeId, expression }),
    async screenshot() {
      const visual = await execute("browser.visual");
      const match = /^data:(image\/[a-z]+);base64,(.+)$/.exec(String(visual.image?.dataUrl ?? ""));
      if (!match) throw new Error("The tab could not be captured.");
      return { mediaType: match[1]!, data: match[2]! };
    },
    async close() {
      await call(bridge, "/close", { scopeId }).catch(() => undefined);
    },
  };
}
