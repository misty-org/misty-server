import type { ModelMessage } from "ai";
import { openedScopes, PAGE_STATE_CHARS, pageStateMessages } from "../src/opened-pages.js";
import { executeTool } from "../src/tool-calls.js";
import type { AgentRun } from "./agent-run.js";

/**
 * Pushed page state (src/opened-pages.ts): the pages this turn opened, shown before the
 * next turn, so the agent acts on them instead of spending a turn asking for each. A run
 * with browser_run reads pages in its programs instead (docs/agents/harness.md, browser_run).
 */
export async function pushOpenedPages(run: AgentRun, toolResults: unknown, messages: ModelMessage[]) {
  const { catalog } = run.tools;
  const openTool = catalog.modelName("screen.open");
  const snapshotTool = catalog.modelName("browser.snapshot");
  if (!openTool || !snapshotTool || catalog.modelName("browser.run")) return;
  const scopes = openedScopes(toolResults as ReadonlyArray<{ toolName?: string; output?: unknown }>, openTool);
  for (const [index, scopeId] of scopes.entries()) {
    const callId = `page-state-${run.modelTurn}-${index}`;
    try {
      const state = await executeTool(run.identity, callId, "browser.snapshot", { scopeId, maxChars: PAGE_STATE_CHARS });
      run.documents.note({ scopeId }, state);
      messages.push(...pageStateMessages(callId, snapshotTool, scopeId, state));
    } catch {
      // The agent can still take the snapshot itself.
    }
  }
}
