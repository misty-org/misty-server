// The browser tools as production runs them. Each one is a device job in
// Misty's own browser (webview.ts), so the step rules, page layer, site checks
// and input are the desktop's own. Only browser_extract has a server half here:
// the device reads the page and the extraction model call is made as the
// server makes it (browser_page_tools.go).
import type { EvalBrowser } from "./webview.js";

export interface ExtractPage {
  url: string;
  title: string;
  markdown: string;
  truncated: boolean;
}

// browserExtractSchemaLimit and browserExtractPageLimit in browser_page_tools.go.
const extractSchemaLimit = 16 << 10;
const extractPageLimit = 120_000;

export class PageTools {
  constructor(
    private readonly browser: EvalBrowser,
    private readonly extract: (instruction: string, schema: unknown, page: ExtractPage) => Promise<unknown>,
  ) {}

  async run(name: string, input: Record<string, any>): Promise<unknown> {
    // The model names the tab by scopeId; the bridge already knows it.
    const { scopeId: _scope, ...args } = input;
    switch (name) {
      case "browser.request_user_action":
        // No person is present in the eval: the task cannot continue past this.
        return { status: "failure", attempted: false, tool_error: { code: "user_unavailable", message: "No user is available in this run. Finish and report what needs the person." } };
      case "browser.extract": {
        const instruction = typeof args.instruction === "string" ? args.instruction.trim() : "";
        const schema = args.schema;
        if (!instruction || instruction.length > 4000) throw new Error("browser_extract needs an instruction");
        if (!schema || typeof schema !== "object" || (schema as { type?: unknown }).type !== "object" || JSON.stringify(schema).length > extractSchemaLimit)
          throw new Error("browser_extract needs a JSON Schema whose top level has type object");
        const page = (await this.browser.execute("browser.extract")) as Partial<ExtractPage>;
        const markdown = String(page.markdown ?? "");
        const source: ExtractPage = {
          url: String(page.url ?? ""), title: String(page.title ?? ""),
          markdown: markdown.slice(0, extractPageLimit), truncated: Boolean(page.truncated) || markdown.length > extractPageLimit,
        };
        const data = await this.extract(instruction, schema, source);
        return { data, url: source.url, title: source.title, pageTruncated: source.truncated, contentTrust: "untrusted-web-page" };
      }
      default:
        try {
          return await this.browser.execute(name, args);
        } catch (error) {
          // The server answers a stale page this way (mcpToolError in misty_mcp_server.go).
          if (String(error).includes("browser_snapshot_stale")) return staleResult;
          throw error;
        }
    }
  }
}

const staleResult = {
  status: "failure", reason: "browser_snapshot_stale", attempted: false,
  message: "The page changed since it was read. No action was dispatched. Read it again with browser_snapshot (or browser_inspect, if that is what you used) and use its new references before deciding the next action.",
};
