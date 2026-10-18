import { jsonSchema, tool, type ModelMessage, type ToolSet } from "ai";
import type { ModelCatalog } from "./model-tools.js";

// agent.tool (docs/plans/agent-harness.md, phases 2 and 3). A run starts in
// answer: everything that reads, searches, fetches pages or works with the
// person's own data. browse, desktop and code add the tools that act on a
// screen or a project; agent.tool.load adds connected apps or sub-agents. The
// model sees only what its current agent.tool allows, so a quick answer never
// wades through browser and desktop tools.

export const toolUseTool = "agent_tool_use";
export const toolLoadTool = "agent_tool_load";

type Toolset = "answer" | "browse" | "desktop" | "code";
type Family = "integration" | "agent";

/** Which agent.tool a catalog tool (Misty name) belongs to; undefined is answer. */
function toolsetOf(name: string): Toolset | Family | undefined {
  if (name.startsWith("browser.workspace.")) return "desktop";
  if (name === "browser.act") return "browse";
  if (name.startsWith("browser.") && !name.startsWith("browser.tab.")) return "browse";
  // Opening the agent's own tab is how a run reads what a fetch can't, so it stays in answer.
  if (name === "screen.open") return undefined;
  if (name.startsWith("code.")) return "code";
  if (name.startsWith("integration.")) return "integration";
  if (name === "agent.delegate" || name === "agent.wait") return "agent";
  return undefined;
}

const toolsetWords: Record<Toolset, string> = {
  answer: "answer: read, search, fetch pages and work with the person's own data (the default)",
  browse: "browse: also use a web browser to click, type, sign in and act on sites",
  desktop: "desktop: also control other apps on the person's computer",
  code: "code: also hand coding in an attached project to the person's coding tool",
};
const familyWords: Record<Family, string> = {
  integration: "integration: the person's connected apps (Gmail, Calendar, Slack, GitHub and others)",
  agent: "agent: sub-agents that work on independent parts of a large task at the same time",
};

/** The run's agent.tool and loaded families, read back from its own tool calls so a replayed run agrees. */
export function toolState(messages: ModelMessage[], initial: Toolset = "answer"): { toolset: Toolset; loaded: Set<Family>; seen: boolean } {
  let toolset = initial;
  let seen = false;
  const loaded = new Set<Family>();
  for (const message of messages) {
    if (message.role !== "assistant" || typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type !== "tool-call") continue;
      const input = (part.input ?? {}) as { tool?: string; family?: string };
      if (part.toolName === toolUseTool && input.tool && input.tool in toolsetWords) (toolset = input.tool as Toolset), (seen = true);
      if (part.toolName === toolLoadTool && input.family && input.family in familyWords) loaded.add(input.family as Family), (seen = true);
    }
  }
  return { toolset, loaded, seen };
}

export function agentTools(catalog: ModelCatalog) {
  const offered = new Set(catalog.tools.map((item) => toolsetOf(item.name)).filter((value): value is Toolset | Family => value !== undefined));
  const toolsets = (["answer", "browse", "desktop", "code"] as const).filter((name) => name === "answer" || offered.has(name));
  // Browser tools exist only once a browser is attached (a screen the agent
  // opened, or a tab the person shared), so such a run starts in browse.
  const initial: Toolset = catalog.tools.some((item) => item.name.startsWith("browser.") && toolsetOf(item.name) === "browse" && item.name !== "browser.request_user_action")
    ? "browse"
    : "answer";
  const families = (["integration", "agent"] as const).filter((name) => offered.has(name));

  /** Model names visible under the state, with the runtime's own tools. */
  const visible = (all: string[], state: { toolset: Toolset; loaded: Set<Family> }): string[] =>
    all.filter((modelName) => {
      const group = toolsetOf(catalog.mistyName(modelName));
      return group === undefined || group === state.toolset || state.loaded.has(group as Family);
    });

  const tools: ToolSet = {};
  if (toolsets.length > 1) {
    tools[toolUseTool] = tool({
      description: `Switch what this run can do, only when the request needs it. ${toolsets.map((name) => toolsetWords[name]).join("; ")}. The new tools are available from your next step.`,
      inputSchema: jsonSchema({ type: "object", required: ["tool"], additionalProperties: false, properties: { tool: { type: "string", enum: toolsets } } }),
      execute: async ({ tool: name }: { tool: Toolset }) => ({ toolset: name, note: `Now in agent.tool.${name}.` }),
    });
  }
  if (families.length) {
    tools[toolLoadTool] = tool({
      description: `Add a group of tools when the task needs it: ${families.map((name) => familyWords[name]).join("; ")}.`,
      inputSchema: jsonSchema({ type: "object", required: ["family"], additionalProperties: false, properties: { family: { type: "string", enum: families } } }),
      execute: async ({ family }: { family: Family }) => ({ loaded: family }),
    });
  }
  const instructions = toolsets.length > 1 || families.length
    ? `Tools: this run starts in agent.tool.${initial}${initial === "answer" ? ", with tools to read, search, fetch pages and work with the person's own data" : ""}. ${toolsets.length > 1 ? `Call ${toolUseTool} to switch when the request needs more: ${toolsets.filter((name) => name !== "answer").join(", ")}. ` : ""}${families.length ? `Call ${toolLoadTool} to add ${families.join(" or ")} tools. ` : ""}Don't switch for something you can answer or read without it.`
    : "";
  return { tools, visible, instructions, initial };
}
