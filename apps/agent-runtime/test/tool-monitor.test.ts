import { describe, expect, it } from "vitest";
import { modelCatalog } from "../src/model-tools.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { toolMonitor } from "../src/tool-monitor.js";

const schema = { type: "object", properties: {} };
const catalog = modelCatalog([
  { name: "notes.search", description: "", inputSchema: schema, readOnly: true },
  { name: "notes.update", description: "", inputSchema: schema },
  { name: "browser.workspace.interact", description: "", inputSchema: schema },
  { name: "browser.workspace.visual", description: "", inputSchema: schema, readOnly: true },
  { name: "screen.open", description: "", inputSchema: schema, readOnly: true },
], ["misty_finish_task"]);

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
    expect(catalog.tools.map((tool) => tool.modelName)).toEqual(["notes_search", "notes_update", "browser_workspace_interact", "browser_workspace_visual", "screen_open"]);
    expect(catalog.mistyName("notes_update")).toBe("notes.update");
    expect(catalog.readOnly("notes_search")).toBe(true);
    expect(modelCatalog([{ name: "misty.finish_task", description: "", inputSchema: schema }], ["misty_finish_task"]).tools[0]?.modelName).toBe("misty_finish_task_2");
  });

  it("lets the model correct a rejected write, then stops when it repeats it unchanged", async () => {
    const { order, monitor, call } = setup(new Set(["first", "second"]));
    await call("first", "notes_update", { success: false, error: "tool_execution_failed: pass space for this change" }, { id: "n1" });
    expect(order.stoppedReason).toBe("");
    expect(order.resume()).toBe(true);
    await call("second", "notes_update", { success: false, error: "tool_execution_failed: pass space for this change" }, { id: "n1" });
    expect(order.stoppedReason).not.toBe("");
    expect(monitor.failures()).toHaveLength(1);
  });

  it("never stops the run for a failed read", async () => {
    const { order, monitor, call } = setup();
    for (const id of ["a", "b", "c"]) {
      await call(id, "notes_search", { success: false, error: "Misty's tool service is temporarily unavailable." }, { query: "launch" });
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
    await call("send", "notes_update", { success: true, output: { status: "uncertain" } });
    expect(order.stoppedReason).not.toBe("");
    expect(monitor.failures()).toHaveLength(1);
  });

  it("declines the rest of a response after a failure without running it", async () => {
    const { order, monitor } = setup(new Set(["write"]));
    const write = { toolCallId: "write", toolName: "notes_update", input: {} };
    const queued = { toolCallId: "queued", toolName: "notes_search", input: {} };
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

  it("requires a fresh screenshot after native workspace input", async () => {
    const { order, monitor, call } = setup();
    await call("click", "browser_workspace_interact", { success: true, output: { ok: true } });
    expect(monitor.activeTools(["notes_search", "browser_workspace_visual"])).toEqual(["browser_workspace_visual"]);
    expect(order.resume()).toBe(true);
    await call("look", "browser_workspace_visual", { success: true, output: { documentId: "screen-2" } });
    expect(monitor.requiredInspection).toBe("");
    expect(monitor.activeTools(["notes_search", "browser_workspace_visual"])).toHaveLength(2);
  });
});
