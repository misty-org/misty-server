import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { modelCatalog } from "../src/model-tools.js";
import { agentTools, toolLoadTool, toolState, toolUseTool } from "../src/toolsets.js";

const schema = { type: "object", properties: {} };
const catalogOf = (names: string[]) => modelCatalog(names.map((name) => ({ name, description: name, inputSchema: schema })), ["agent_finish", toolUseTool, toolLoadTool]);
const call = (toolName: string, input: unknown): ModelMessage => ({ role: "assistant", content: [{ type: "tool-call", toolCallId: toolName, toolName, input }] });

describe("agent.tool", () => {
  const catalog = catalogOf(["web.search", "web.fetch", "note.search", "screen.open", "screen.look", "code.delegate", "integration.search", "integration.execute", "agent.delegate", "browser.tab.list"]);
  const tools = agentTools(catalog);
  const all = [...catalog.tools.map((item) => item.modelName), ...Object.keys(tools.tools)];

  it("starts in answer, with its own tab one call away, and hides projects, apps and sub-agents", () => {
    expect(tools.initial).toBe("answer");
    const shown = tools.visible(all, toolState([], tools.initial));
    expect(shown).toEqual(expect.arrayContaining(["web_search", "web_fetch", "note_search", "screen_look", "screen_open", "browser_tab_list", toolUseTool, toolLoadTool]));
    for (const hidden of ["code_delegate", "integration_search", "integration_execute", "agent_delegate"]) expect(shown).not.toContain(hidden);
  });

  it("follows switches and loads in the transcript", () => {
    const state = toolState([call(toolUseTool, { tool: "browse" }), call(toolLoadTool, { family: "integration" })], tools.initial);
    const shown = tools.visible(all, state);
    expect(shown).toEqual(expect.arrayContaining(["screen_open", "integration_search", "integration_execute"]));
    expect(shown).not.toContain("code_delegate");
    expect(shown).not.toContain("agent_delegate");
  });

  it("offers only the toolsets and families the run has", () => {
    const small = agentTools(catalogOf(["web.search", "note.search"]));
    expect(small.tools).toEqual({});
    expect(small.instructions).toBe("");
  });

  it("starts in browse when a browser is already attached", () => {
    const attached = agentTools(catalogOf(["web.search", "browser.snapshot", "browser.steps", "browser.request_user_action"]));
    expect(attached.initial).toBe("browse");
    expect(attached.visible(["web_search", "browser_snapshot", "browser_steps"], toolState([], attached.initial))).toHaveLength(3);
  });
});
