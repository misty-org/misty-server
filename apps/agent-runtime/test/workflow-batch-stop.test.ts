import { beforeEach, expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({
  completions: [] as Array<Record<string, unknown>>,
  executed: [] as string[],
  queued: true,
  result: { status: "uncertain", reason: "Send response lost" } as Record<string, unknown>,
}));
vi.mock("workflow", () => ({
  getWorkflowMetadata: () => ({ workflowRunId: "pinned-runtime" }),
  FatalError: class extends Error {}, RetryableError: class extends Error {},
  defineHook: () => ({}),
}));
vi.mock("../src/control-plane.js", () => ({
  controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, unknown>) => {
    if (operation === "context") return { model_id: "fixture/model", system: "", prompt: "Send then record it", allowed_tools: ["inbox.send", "notes.create"], required_tools: [] };
    if (operation === "complete") fixture.completions.push(body);
    if (operation === "budget") return { version: 1, remaining_ms: 1800000, active: true, deadline: new Date(Date.now()+1800000).toISOString() };
    if (operation === "mcp-token") return {};
    return { accepted: true };
  },
}));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: ["inbox.send", "notes.create"].map(name => ({ name, description: name, inputSchema: { type: "object", properties: {} } })) }),
  requestMCPToolExecution: async (_context: unknown, _access: unknown, _id: string, name: string) => {
    fixture.executed.push(name);
    return { result: fixture.result };
  },
}));
vi.mock("@ai-sdk/workflow", () => ({
  WorkflowAgent: class {
    constructor(private options: any) {}
    async stream() {
      const entries = Object.entries(this.options.tools).filter(([name]) => name !== "misty_discover_capabilities") as Array<[string, any]>;
      const first = { toolCallId: "send-call", toolName: entries[0]![0], input: {} };
      const second = { toolCallId: "note-call", toolName: entries[1]![0], input: {} };
      await this.options.onToolExecutionStart({ toolCall: first });
      // Model responses can contain several calls. Start their lifecycles before
      // the first effect finishes, just as concurrent adapter dispatch does.
      const later = fixture.queued ? this.options.onToolExecutionStart({ toolCall: second }).then(async () => {
        await entries[1]![1].execute({}, { toolCallId: second.toolCallId });
      }) : Promise.resolve();
      const output = await entries[0]![1].execute({}, { toolCallId: first.toolCallId });
      await this.options.onToolExecutionEnd({ toolCall: first, success: true, durationMs: 1, output });
      await later;
      if (fixture.queued) throw new Error("dependent action should have been stopped");
      return {
        steps: [{ text: "", content: [{ type: "tool-result", toolName: first.toolName, output }] }],
        messages: [], finishReason: "tool-calls", totalUsage: { inputTokens: 1, outputTokens: 1 },
      };
    }
  },
}));
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
beforeEach(() => { fixture.completions=[]; fixture.executed=[]; fixture.queued=true; });
for (const output of [{ status: "uncertain", reason: "Send response lost" }, { denied: true, reason: "creator_denied" }, { status: "user_intervention_required", action: "sign_in" }]) {
  it(`stops a queued dependent action after ${output.status ?? "denial"} and records incomplete work`, async () => {
    fixture.result = output;
    expect(await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://api.test" })).toMatchObject({ incomplete: true });
    expect(fixture.executed).toEqual(["inbox.send"]);
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "incomplete", error_code: "tool_sequence_stopped" })]);
  });
}

it("does not spend another model turn after a single denied action", async () => {
  fixture.queued = false;
  fixture.result = { denied: true, reason: "creator_denied" };
  expect(await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://api.test" })).toMatchObject({ incomplete: true });
  expect(fixture.executed).toEqual(["inbox.send"]);
  expect(fixture.completions[0]?.status).not.toBe("success");
});
