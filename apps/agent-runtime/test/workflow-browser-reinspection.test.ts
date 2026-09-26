import { beforeEach, expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({ completions: [] as any[], actions: [] as string[], active: [] as string[][], turn: 0, omitRetry: false }));
vi.mock("workflow", () => ({ getWorkflowMetadata: () => ({ workflowRunId: "test-runtime" }), FatalError: class extends Error {}, RetryableError: class extends Error {}, defineHook: () => ({}) }));
vi.mock("../src/control-plane.js", () => ({ controlPlaneRequest: async (_: unknown, operation: string, body: unknown) => {
  if (operation === "context") return { model_id: "fixture/model", system: "", prompt: "Click the inspected target", allowed_tools: ["browser.click", "browser.inspect"], required_tools: ["browser.click"] };
  if (operation === "complete") fixture.completions.push(body);
  if (operation === "budget") return { version: 1, remaining_ms: 1800000, active: true, deadline: new Date(Date.now()+1800000).toISOString() };
  return {};
} }));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: ["browser.click", "browser.inspect"].map(name => ({ name, description: name, inputSchema: { type: "object", properties: {} } })) }),
  requestMCPToolExecution: async (_: unknown, __: unknown, ___: unknown, name: string) => {
    fixture.actions.push(name);
    return { result: fixture.actions.length === 1 ? { status: "failure", reason: "browser_snapshot_stale", attempted: false } : { ok: true } };
  },
}));
vi.mock("@ai-sdk/workflow", () => ({ WorkflowAgent: class {
  constructor(private options: any) {}
  async stream() {
    const turn = fixture.turn++;
    const entries = Object.entries(this.options.tools).filter(([key]) => key !== "misty_discover_capabilities") as Array<[string, any]>;
    fixture.active.push(this.options.prepareStep().activeTools);
    if (turn === 3 || (fixture.omitRetry && turn === 2)) return { steps: [{ text: "Done", content: [] }], messages: [], finishReason: "stop", totalUsage: {} };
    const [name, tool] = entries[turn === 1 ? 1 : 0]!;
    const call = { toolCallId: `call-${turn}`, toolName: name, input: { elementRef: `ref-${turn}` } };
    await this.options.onToolExecutionStart({ toolCall: call });
    const output = await tool.execute(call.input, { toolCallId: call.toolCallId });
    await this.options.onToolExecutionEnd({ toolCall: call, success: true, durationMs: 1, output });
    return { steps: [{ text: "", content: [{ type: "tool-result", toolName: name, output }] }], messages: [], finishReason: "tool-calls", totalUsage: {} };
  }
} }));
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
beforeEach(() => { fixture.completions=[]; fixture.actions=[]; fixture.active=[]; fixture.turn=0; fixture.omitRetry=false; });
it("requires a new inspection turn and then permits a freshly planned browser action", async () => {
  await runSpaceTaskAgent({ mistyRunId: "run-test", controlPlaneURL: "https://api.test" });
  expect(fixture.actions).toEqual(["browser.click", "browser.inspect", "browser.click"]);
  expect(fixture.active[1]).toHaveLength(1);
  expect(fixture.active[2]!.length).toBeGreaterThan(1);
  expect(fixture.completions[0].status).toBe("success");
});
it("does not count the rejected click as a completed required effect", async () => {
  fixture.omitRetry = true;
  await runSpaceTaskAgent({ mistyRunId: "run-test", controlPlaneURL: "https://api.test" });
  expect(fixture.actions).toEqual(["browser.click", "browser.inspect"]);
  expect(fixture.completions[0].status).toBe("incomplete");
});
