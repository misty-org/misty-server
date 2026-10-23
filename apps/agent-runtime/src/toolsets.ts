import { jsonSchema, tool, type ModelMessage, type ToolSet } from "ai";
import type { ModelCatalog } from "./model-tools.js";

// agent.tool (docs/agents/harness.md, Tool surface). A run starts in
// answer: everything that reads, searches, fetches pages or works with the
// person's own data. browse, desktop and code add the tools that act on a
// screen or a project; agent.tool.load adds connected apps or sub-agents. The
// model sees only what its current agent.tool allows, so a quick answer never
// wades through browser and desktop tools.

export const toolUseTool = "agent_tool_use";
export const toolLoadTool = "agent_tool_load";

type Toolset = "answer" | "browse" | "desktop" | "code";
type Family = "integration" | "agent" | "misty";

// The person's Misty data and its social and workflow tools: about 50 of a run's 65 tools and
// 9,000 tokens on every turn of a web task that never uses them (docs/agents/harness.md, Tool surface).
// They load up front when the request is about Misty's own things, and otherwise on request.
const mistyData = [
  "memory.workflow.", "memory.pref.", "drawing.", "note.", "thread.", "file.", "voice.", "message.", "space.",
  "bookmark.", "item.", "weather.", "search.all", "agent.configure", "agent.request", "agent.directory", "agent.list",
];
const mistyRequest =
  /\b(my|our|this|the person's) (own )?(notes?|notebook|drawings?|sketch(es)?|files?|documents in misty|chats?|threads?|messages in misty|spaces?|bookmarks?|saved workflows?|workflows?|memory|memories|preferences|recordings?|voice notes?|schedules?|agents?)\b|\b(in|on|from|to) misty\b|\bmisty('s)? (notes?|files?|chat|spaces?|drawings?|bookmarks?)\b|\bremember (that|this|my|me)\b|\bweather\b|\b(a|the|this) space\b/i;

// A request that names an app people connect (docs/agents/harness.md, Tool surface): the
// app tools load up front, so the first action comes a turn after the request, not three.
const appRequest =
  /\b(e-?mail|gmail|inbox|outlook|calendar|meeting invite|slack|github|pull request|notion|linear|jira|asana|trello|google (docs?|sheets?|drive|calendar)|drive folder|spreadsheet in (my )?drive|hubspot|salesforce|dropbox|figma|discord|my (contacts|events|repos?|channels?))\b/i;

/** Which agent.tool a catalog tool (Misty name) belongs to; undefined is answer. */
function toolsetOf(name: string): Toolset | Family | undefined {
  // Opening the agent's own tab is how a run reads what a fetch can't, so it stays in answer, and
  // so does a program against those tabs (docs/agents/harness.md, browser_run). Checked
  // before the browser prefix, which would put browser.run behind browse with the step tools.
  if (name === "screen.open" || name === "browser.run") return undefined;
  if (name.startsWith("browser.workspace.")) return "desktop";
  if (name.startsWith("browser.") && !name.startsWith("browser.tab.")) return "browse";
  if (name.startsWith("code.")) return "code";
  if (name.startsWith("integration.")) return "integration";
  if (name === "agent.delegate" || name === "agent.wait") return "agent";
  if (mistyData.some((prefix) => name === prefix || name.startsWith(prefix))) return "misty";
  return undefined;
}

const toolsetWords: Record<Toolset, string> = {
  answer: "answer: read, search, fetch pages and work with the person's own data (the default)",
  browse: "browse: also the single-step page tools (snapshot, steps, forms, page skills, screen agent) for what a browser_run program cannot do",
  desktop: "desktop: also control other apps on the person's computer",
  code: "code: also hand coding in an attached project to the person's coding tool",
};
const familyWords: Record<Family, string> = {
  integration: "integration: the person's connected apps (Gmail, Calendar, Slack, GitHub and others)",
  agent: "agent: sub-agents that work on independent parts of a large task at the same time",
  misty: "misty: the person's own Misty notes, drawings, files, chats, Spaces, memory, saved workflows, bookmarks and weather",
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

export function agentTools(catalog: ModelCatalog, request = "") {
  const offered = new Set(catalog.tools.map((item) => toolsetOf(item.name)).filter((value): value is Toolset | Family => value !== undefined));
  const toolsets = (["answer", "browse", "desktop", "code"] as const).filter((name) => name === "answer" || offered.has(name));
  // Browser tools exist only once a browser is attached (a screen the agent
  // opened, or a tab the person shared), so such a run starts in browse, unless it
  // has browser_run: then pages are worked with programs, and the 17 single-step
  // tools wait behind browse (docs/agents/harness.md, Tool surface).
  const programs = catalog.tools.some((item) => item.name === "browser.run");
  const initial: Toolset = !programs && catalog.tools.some((item) => item.name.startsWith("browser.") && toolsetOf(item.name) === "browse" && item.name !== "browser.request_user_action")
    ? "browse"
    : "answer";
  const families = (["integration", "agent", "misty"] as const).filter((name) => offered.has(name));
  // A request about Misty's own things starts with them loaded.
  const preloaded = new Set<Family>(offered.has("misty") && mistyRequest.test(request) ? ["misty"] : []);
  if (offered.has("integration") && appRequest.test(request)) preloaded.add("integration");
  // A run with its own tabs splits a broad task across sub-agents (docs/agents/harness.md,
  // Sub-agents and the notebook); behind agent_tool_load they were never used, and 10-to-20-item tasks ran one by one.
  if (offered.has("agent") && catalog.tools.some((item) => item.name === "screen.open")) preloaded.add("agent");

  /** Model names visible under the state, with the runtime's own tools. */
  const visible = (all: string[], state: { toolset: Toolset; loaded: Set<Family> }): string[] =>
    all.filter((modelName) => {
      // browser.tab.* act on the person's own tabs, which an agent never touches (its own tabs
      // are listed and opened by browser_run): offered, they only fail, and a failed open ends the run.
      if (programs && catalog.mistyName(modelName).startsWith("browser.tab.")) return false;
      const group = toolsetOf(catalog.mistyName(modelName));
      return group === undefined || group === state.toolset || state.loaded.has(group as Family) || preloaded.has(group as Family);
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
