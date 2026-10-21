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

  it("starts in answer, with its own tab one call away and sub-agents ready, and hides projects and apps", () => {
    expect(tools.initial).toBe("answer");
    const shown = tools.visible(all, toolState([], tools.initial));
    expect(shown).toEqual(expect.arrayContaining(["web_search", "web_fetch", "screen_look", "screen_open", "browser_tab_list", "agent_delegate", toolUseTool, toolLoadTool]));
    for (const hidden of ["code_delegate", "integration_search", "integration_execute", "note_search"]) expect(shown).not.toContain(hidden);
  });

  it("keeps sub-agents behind a load when the run can't open its own tabs", () => {
    const noTabs = catalogOf(["web.search", "web.fetch", "agent.delegate"]);
    const loaded = agentTools(noTabs);
    const names = [...noTabs.tools.map((item) => item.modelName), ...Object.keys(loaded.tools)];
    expect(loaded.visible(names, toolState([], loaded.initial))).not.toContain("agent_delegate");
    expect(loaded.visible(names, toolState([call(toolLoadTool, { family: "agent" })], loaded.initial))).toContain("agent_delegate");
  });

  it("starts with Misty's own tools loaded when the request is about them", () => {
    const about = agentTools(catalog, "Summarize my notes from last week");
    expect(about.visible(all, toolState([], about.initial))).toContain("note_search");
    const web = agentTools(catalog, "Find the cheapest Nike running shoes under $200");
    expect(web.visible(all, toolState([], web.initial))).not.toContain("note_search");
    expect(web.visible(all, toolState([call(toolLoadTool, { family: "misty" })], web.initial))).toContain("note_search");
  });

  it("follows switches and loads in the transcript", () => {
    const state = toolState([call(toolUseTool, { tool: "browse" }), call(toolLoadTool, { family: "integration" })], tools.initial);
    const shown = tools.visible(all, state);
    expect(shown).toEqual(expect.arrayContaining(["screen_open", "integration_search", "integration_execute"]));
    expect(shown).not.toContain("code_delegate");
  });

  it("offers only the toolsets and families the run has", () => {
    const small = agentTools(catalogOf(["web.search", "web.fetch"]));
    expect(small.tools).toEqual({});
    expect(small.instructions).toBe("");
  });

  it("starts in browse when a browser is already attached", () => {
    const attached = agentTools(catalogOf(["web.search", "browser.snapshot", "browser.steps", "browser.request_user_action"]));
    expect(attached.initial).toBe("browse");
    expect(attached.visible(["web_search", "browser_snapshot", "browser_steps"], toolState([], attached.initial))).toHaveLength(3);
  });

  it("offers browser_run in answer and keeps the step tools behind browse", () => {
    const programs = agentTools(catalogOf(["web.search", "screen.open", "browser.run", "browser.snapshot", "browser.steps"]));
    expect(programs.initial).toBe("answer");
    expect(programs.visible(["web_search", "screen_open", "browser_run", "browser_snapshot", "browser_steps"], toolState([], programs.initial))).toEqual(["web_search", "screen_open", "browser_run"]);
  });
});
