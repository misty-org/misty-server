import { beforeEach, expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({
  calls: [] as unknown[][], controls: [] as { operation: string; body: Record<string, any> }[],
  completions: [] as Record<string, unknown>[], turn: 0, partial: false,
  namespace: "10000000-0000-4000-8000-000000000004",
}));
vi.mock("workflow", () => ({ getWorkflowMetadata: () => ({ workflowRunId: "routine-worker" }), FatalError: class extends Error {}, RetryableError: class extends Error {}, defineHook: () => ({}), sleep: vi.fn() }));
vi.mock("@ai-sdk/workflow", () => ({ WorkflowAgent: class {
  constructor(private settings: any) { expect(Object.keys(settings.tools)).toEqual(["capability_0"]); }
  async stream(input: any) {
    fixture.turn++;
    if (fixture.turn === 1) {
      const toolCall = { toolCallId: "read_1", toolName: "capability_0", input: {} };
      await this.settings.onToolExecutionStart({ toolCall });
      const output = await this.settings.tools.capability_0.execute({}, { toolCallId: toolCall.toolCallId });
      await this.settings.onToolExecutionEnd({ toolCall, success: true, output });
    }
    const finishReason = fixture.turn === 1 ? "tool-calls" : "stop";
    await this.settings.onStepEnd({ usage: { inputTokens: 3, outputTokens: 4, totalTokens: 7, inputTokenDetails: {}, outputTokenDetails: {} }, finishReason });
    return { steps: [{}], messages: input.messages, finishReason, output: fixture.turn === 1 ? undefined : { summary: "private summary" } };
  }
} }));
vi.mock("../src/control-plane.js", () => ({ controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, any>) => {
  fixture.controls.push({ operation, body });
  const pin = { capability: "habits.list", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000002", targetRevision: 1 };
  if (operation === "context") return { allowed_tools: ["sdk.read", "sdk.save"], routine_execution: {
    routineId: "10000000-0000-4000-8000-000000000001", version: 1, runId: "run_routine", trigger: {},
    definition: { protocol: 1, name: "Agent then save", trigger: { kind: "manual" }, budget: {}, steps: [
      { id: "summarize", label: "Summarize", kind: "agent", actions: [pin], maxTurns: 2, outputSchema: { type: "object" }, prompt: { kind: "literal", value: "Summarize habits" } },
      { id: "save", label: "Save", kind: "capability", action: pin, input: { kind: "reference", source: { kind: "step", stepId: "summarize" }, path: [] } },
    ] },
    bindings: [{ stepId: "save", callId: "10000000-0000-4000-8000-000000000003", toolName: "sdk.save" }],
    agentBindings: [{ stepId: "summarize", callNamespace: fixture.namespace, tools: [{ toolName: "sdk.read", action: pin }] }],
  } };
  if (operation === "routine-agent") {
    if (body.phase === "open") return { state: "running", protocol: 1, stepId: "summarize", callNamespace: fixture.namespace, prompt: "Summarize habits", modelId: "pinned-model", maxTurns: 2 };
    if (body.phase === "finish") return { status: "success", result: fixture.partial ? null : body.result.output, partial: fixture.partial, evidence: [] };
  }
  if (operation === "budget" || operation === "routine-agent" && body.phase === "model_start") return { version: 1, active: true, remaining_ms: 1700000, deadline: new Date(Date.now() + 1700000).toISOString() };
  if (operation === "complete") fixture.completions.push(body);
  return { accepted: true };
} }));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: ["sdk.read", "sdk.save"].map(name => ({ name, description: name, inputSchema: { type: "object" } })) }),
  requestMCPToolExecution: async (...args: unknown[]) => { fixture.calls.push(args); return { result: { status: "success", result: { habit: "private habit data" }, partial: false, evidence: [] } }; },
}));
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
beforeEach(() => { fixture.calls = []; fixture.controls = []; fixture.completions = []; fixture.turn = 0; fixture.partial = false; });
it("negotiates and runs the bounded agent through shared effects and Go-confirmed output", async () => {
  const result = await runSpaceTaskAgent({ mistyRunId: "run_routine", controlPlaneURL: "https://control.invalid" });
  expect(result).toMatchObject({ state: "completed" });
  expect(fixture.calls).toHaveLength(2);
  expect(JSON.stringify(fixture.calls[0])).toContain(`${fixture.namespace}:read_1`);
  expect(JSON.stringify(fixture.calls[1])).toContain("private summary");
  expect(fixture.controls.find(c => c.operation === "context")?.body).toEqual({ routine_protocol: 1, routine_agent_protocol: 1, routine_wait_protocol: 1 });
  const phases = fixture.controls.filter(c => c.operation === "routine-agent");
  expect(phases.map(c => c.body.phase)).toEqual(["open", "model_start", "model_finish", "model_start", "model_finish", "finish"]);
  expect(phases.at(-1)?.body.result).toMatchObject({ state: "completed", modelTurns: 2, capabilityCalls: 1 });
  expect(JSON.stringify(fixture.controls.filter(c => c.operation === "events"))).not.toContain("private");
});
it("halts dependent effects when Go reports partial work despite a complete model answer", async () => {
  fixture.partial = true;
  const result = await runSpaceTaskAgent({ mistyRunId: "run_routine", controlPlaneURL: "https://control.invalid" });
  expect(result).toMatchObject({ state: "partial", steps: [{ stepId: "summarize", state: "partial", callId: fixture.namespace }, { stepId: "save", state: "not_run" }] });
  expect(fixture.calls).toHaveLength(1);
  expect(fixture.completions.at(-1)).toMatchObject({ status: "incomplete" });
});
