import { describe, expect, it } from "vitest";
import { modelCatalog } from "../src/model-tools.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { toolMonitor } from "../src/tool-monitor.js";

const schema = { type: "object", properties: {} };
const catalog = modelCatalog([
  { name: "note.search", description: "", inputSchema: schema, readOnly: true },
  { name: "note.update", description: "", inputSchema: schema },
  { name: "browser.workspace.interact", description: "", inputSchema: schema },
  { name: "browser.workspace.visual", description: "", inputSchema: schema, readOnly: true },
  { name: "screen.open", description: "", inputSchema: schema, readOnly: true },
], ["agent_finish"]);

function setup(rejected = new Set<string>()) {
  const order = serialToolLifecycle();
  const monitor = toolMonitor({ catalog, order, rejected, checkpoint: async () => {} });
  const call = async (id: string, toolName: string, end: { success: boolean; output?: unknown; error?: unknown }, input: unknown = {}) => {
    const toolCall = { toolCallId: id, toolName, input };
    await monitor.onStart({ toolCall });
    await monitor.onEnd({ toolCall, durationMs: 1, ...end });
  };
  return { order, monitor, call };
}

describe("tool monitor", () => {
  it("shows the model Misty's tool names with dots as underscores", () => {
    expect(catalog.tools.map((tool) => tool.modelName)).toEqual(["note_search", "note_update", "browser_workspace_interact", "browser_workspace_visual", "screen_open"]);
    expect(catalog.mistyName("note_update")).toBe("note.update");
    expect(catalog.readOnly("note_search")).toBe(true);
    expect(modelCatalog([{ name: "agent.finish", description: "", inputSchema: schema }], ["agent_finish"]).tools[0]?.modelName).toBe("agent_finish_2");
  });

  it("checkpoints a read once, on finish, and a write on start and finish", async () => {
    const order = serialToolLifecycle();
    const saved: string[] = [];
    const monitor = toolMonitor({ catalog, order, rejected: new Set(), checkpoint: async (event) => { saved.push(`${event.node_id}:${event.state}`); } });
    for (const [id, toolName] of [["read", "note_search"], ["write", "note_update"]] as const) {
      const toolCall = { toolCallId: id, toolName, input: {} };
      await monitor.onStart({ toolCall });
      await monitor.onEnd({ toolCall, durationMs: 1, success: true, output: { ok: true } });
    }
    expect(saved).toEqual(["tool:read:completed", "tool:write:running", "tool:write:completed"]);
  });

  it("lets the model correct a rejected write, then stops when it repeats it unchanged", async () => {
    const { order, monitor, call } = setup(new Set(["first", "second"]));
    await call("first", "note_update", { success: false, error: "tool_execution_failed: pass space for this change" }, { id: "n1" });
    expect(order.stoppedReason).toBe("");
    expect(order.resume()).toBe(true);
    await call("second", "note_update", { success: false, error: "tool_execution_failed: pass space for this change" }, { id: "n1" });
    expect(order.stoppedReason).not.toBe("");
    expect(monitor.failures()).toHaveLength(1);
  });

  it("never stops the run for a failed read", async () => {
    const { order, monitor, call } = setup();
    for (const id of ["a", "b", "c"]) {
      await call(id, "note_search", { success: false, error: "Misty's tool service is temporarily unavailable." }, { query: "launch" });
      expect(order.resume()).toBe(true);
    }
    expect(order.stoppedReason).toBe("");
    expect(monitor.failures()).toHaveLength(0);
  });

  it("ends the response without a failure when it hands off to a screen", async () => {
    const { order, monitor, call } = setup();
    await call("screen", "screen_open", { success: true, output: { status: "screen_requested", message: "Opening a browser." } });
    expect(order.stoppedReason).toBe("Opening a browser.");
    expect(monitor.handoff).toBe("Opening a browser.");
    expect(monitor.failures()).toHaveLength(0);
  });

  it("stops when a write's outcome is unknown", async () => {
    const { order, monitor, call } = setup();
    await call("send", "note_update", { success: true, output: { status: "uncertain" } });
    expect(order.stoppedReason).not.toBe("");
    expect(monitor.failures()).toHaveLength(1);
  });

  it("looks again after an uncertain page action, but stops on an uncertain consequential one", async () => {
    const pages = modelCatalog([
      { name: "browser.snapshot", description: "", inputSchema: schema, readOnly: true },
      { name: "browser.steps", description: "", inputSchema: schema },
    ], ["agent_finish"]);
    for (const [input, stops] of [[{ steps: [{ action: "click", ref: "e4" }] }, false], [{ steps: [{ action: "click", ref: "e9", allowConsequential: true }] }, true]] as const) {
      const order = serialToolLifecycle();
      const monitor = toolMonitor({ catalog: pages, order, rejected: new Set(), checkpoint: async () => {} });
      const toolCall = { toolCallId: "act", toolName: "browser_steps", input };
      await monitor.onStart({ toolCall });
      await monitor.onEnd({ toolCall, durationMs: 1, success: true, output: { status: "uncertain" } });
      expect(order.stoppedReason !== "").toBe(stops);
      if (!stops) expect(monitor.requiredInspection).toBe("browser.snapshot");
    }
  });

  it("plans around an own tab that did not open, twice, but not the person's tab", async () => {
    const { order, monitor, call } = setup();
    for (const id of ["a", "b"]) {
      await call(id, "screen_open", { success: true, output: { status: "uncertain" } }, { url: "https://depop.com/x", newTab: true });
      expect(order.stoppedReason).toBe("");
      expect(order.resume()).toBe(true);
    }
    await call("c", "screen_open", { success: true, output: { status: "uncertain" } }, { url: "https://depop.com/x" });
    expect(order.stoppedReason).not.toBe("");
    const person = setup();
    await person.call("d", "screen_open", { success: true, output: { status: "uncertain" } }, { target: "current_tab" });
    expect(person.order.stoppedReason).not.toBe("");
    expect(monitor.failures()).toHaveLength(1);
  });

  it("plans around a request for the person that nobody answered", async () => {
    const pages = modelCatalog([{ name: "browser.request_user_action", description: "", inputSchema: schema }], ["agent_finish"]);
    const order = serialToolLifecycle();
    const monitor = toolMonitor({ catalog: pages, order, rejected: new Set(), checkpoint: async () => {} });
    const toolCall = { toolCallId: "ask", toolName: "browser_request_user_action", input: { reason: "Cloudflare check" } };
    await monitor.onStart({ toolCall });
    await monitor.onEnd({ toolCall, durationMs: 1, success: false, error: "The tool could not complete this action." });
    expect(order.stoppedReason).toBe("");
    expect(monitor.failures()).toHaveLength(0);
  });

  it("declines the rest of a response after a failure without running it", async () => {
    const { order, monitor } = setup(new Set(["write"]));
    const write = { toolCallId: "write", toolName: "note_update", input: {} };
    const queued = { toolCallId: "queued", toolName: "note_search", input: {} };
    await monitor.onStart({ toolCall: write });
    const later = monitor.onStart({ toolCall: queued });
    await monitor.onEnd({ toolCall: write, durationMs: 1, success: false, error: "tool_execution_failed: invalid date" });
    await later;
    expect(order.declined("queued")).toContain("tool_not_attempted");
    await monitor.onEnd({ toolCall: queued, durationMs: 0, success: false, error: order.declined("queued") });
    expect(order.stoppedReason).toBe("");
    expect(order.resume()).toBe(true);
    expect(order.declined("queued")).toBeUndefined();
  });
});
