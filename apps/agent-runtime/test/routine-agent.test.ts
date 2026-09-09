import { beforeEach, expect, it, vi } from "vitest";
import type { RoutineAgentInput, RoutineAgentPorts } from "../src/routine-agent.js";
const fixture = vi.hoisted(() => ({
  settings: undefined as any,
  streams: [] as any[],
  mode: "normal",
  turn: 0,
}));
vi.mock("@ai-sdk/workflow", () => ({ WorkflowAgent: class {
  constructor(settings: unknown) { fixture.settings = settings; }
  async stream(input: any) {
    fixture.streams.push(input);fixture.turn++;
    const settings = fixture.settings;
    await settings.experimental_onStepStart({ stepNumber: 0 });
    if (fixture.mode === "interrupt") await input.onAbort();
    const toolsTurn = fixture.mode === "limit" || fixture.turn === 1;
    const transcript = [...input.messages];
    if (toolsTurn) {
      const invoke = async (id: string) => {
        const toolCall = { toolCallId: id, toolName: "capability_0", input: { note: "private context" } };
        await settings.onToolExecutionStart({ toolCall });
        const output = await settings.tools.capability_0.execute(toolCall.input, { toolCallId: id });
        await settings.onToolExecutionEnd({ toolCall, success: true, output });
        transcript.push({ role: "assistant", content: [{ type: "tool-call", toolCallId: id, toolName: "capability_0", input: toolCall.input }] });
        transcript.push({ role: "tool", content: [{ type: "tool-result", toolCallId: id, toolName: "capability_0", output: { type: "json", value: output } }] });
      };
      if (fixture.mode === "queued") await Promise.all([invoke("call_one"), invoke("call_two")]);
      else await invoke(`call_${fixture.turn}`);
    }
    const finishReason = toolsTurn ? "tool-calls" : "stop";
    await settings.onStepEnd({ usage: { inputTokens: 2, outputTokens: 3, totalTokens: 5, inputTokenDetails: {}, outputTokenDetails: {} }, finishReason });
    return { messages: transcript, steps: [{}], finishReason, output: toolsTurn ? undefined : { summary: "private summary" } };
  }
} }));
import { runRoutineAgent, routineAgentCallID } from "../src/routine-agent.js";
const pin = { capability: "habits.list", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000001", targetRevision: 1 };
const namespace = "10000000-0000-4000-8000-000000000002";
const input = (): RoutineAgentInput => ({
  step: { id: "summarize", kind: "agent", label: "Summarize", prompt: { kind: "literal", value: "Summarize" }, actions: [pin], maxTurns: 2, outputSchema: { type: "object", properties: { summary: { type: "string" } }, required: ["summary"] } },
  binding: { stepId: "summarize", callNamespace: namespace, tools: [{ toolName: "sdk.read", action: pin }] },
  prompt: "Summarize", modelId: "model-fixture",
  catalog: ["sdk.read", "sdk.unrelated"].map(name => ({ name, description: name, inputSchema: { type: "object" } })),
});
const ports = () => ({
  beginModel: vi.fn(async () => ({ version: 1 as const, active: true, remaining_ms: 10000, deadline: new Date(Date.now() + 10000).toISOString() })),
  finishModel: vi.fn(async () => {}),
  executeCapability: vi.fn(async () => ({ status: "success", result: { read: true }, evidence: [], partial: false })),
});
beforeEach(() => { fixture.settings = undefined; fixture.streams = []; fixture.mode = "normal"; fixture.turn = 0; });
it("uses only pinned tools, preserves transcript and reports bounded usage", async () => {
  const execution = ports();const result = await runRoutineAgent(input(), execution);
  expect(result).toMatchObject({ state: "completed", modelTurns: 2, capabilityCalls: 1, output: { summary: "private summary" }, usage: { totalTokens: 10 } });
  expect(Object.keys(fixture.settings.tools)).toEqual(["capability_0"]);
  expect(execution.executeCapability).toHaveBeenCalledWith(`${namespace}:call_1`, "sdk.read", { note: "private context" });
  expect(fixture.streams[1].messages.some((message: any) => message.role === "tool")).toBe(true);
  expect(execution.beginModel.mock.calls).toEqual([[`model:routine:${namespace}:1`], [`model:routine:${namespace}:2`]]);
  expect(JSON.stringify(execution.finishModel.mock.calls)).not.toContain("private");
});
it.each(["uncertain", "partial", "failure"])("stops queued tools on %s before another effect", async state => {
  fixture.mode = "queued";
  const execution = ports();
  execution.executeCapability.mockImplementation(async () => (state === "uncertain"
    ? { status: "uncertain", effectId: namespace, reason: "Unconfirmed", evidence: [] }
    : state === "failure" ? { status: "failure", code: "denied", message: "Not allowed", retryable: false }
    : { status: "success", result: {}, partial: true, evidence: [] }) as any);
  const result = await runRoutineAgent(input(), execution);
  expect(result.state).toBe(state === "failure" ? "failed" : state);
  expect(execution.executeCapability).toHaveBeenCalledTimes(1);
  expect(result.output).toBeUndefined();
});
it("counts turns across the complete transcript and stops at the step limit", async () => {
  fixture.mode = "limit";const execution = ports();const result = await runRoutineAgent(input(), execution);
  expect(result).toMatchObject({ state: "failed", code: "routine_agent_model_turn_limit", modelTurns: 2 });
  expect(execution.beginModel).toHaveBeenCalledTimes(2);
});
it("does not run a model after admission or budget is lost", async () => {
  const execution = ports();execution.beginModel.mockRejectedValue(new Error("revoked"));
  expect(await runRoutineAgent(input(), execution)).toMatchObject({ state: "failed", modelTurns: 0 });
  expect(fixture.streams).toHaveLength(0);
  expect(execution.executeCapability).not.toHaveBeenCalled();
});
it("preflights all pinned tools before any model work", async () => {
  const execution = ports();await expect(runRoutineAgent({ ...input(), catalog: [] }, execution)).rejects.toThrow("routine_agent_capability_unavailable");
  expect(execution.beginModel).not.toHaveBeenCalled();
});
it("reuses the same admitted effect namespace after recovery", async () => {
  const execution = ports();const results = new Map<string, unknown>();let writes = 0;
  const adapter: RoutineAgentPorts = { ...execution, executeCapability: async id => {
    if (!results.has(id)) { writes++;results.set(id, { status: "success", result: {}, partial: false, evidence: [] }); }
    return results.get(id);
  } };
  await runRoutineAgent(input(), adapter);fixture.turn = 0;await runRoutineAgent(input(), adapter);
  expect(writes).toBe(1);
  expect(() => routineAgentCallID(namespace, "another:namespace")).toThrow("invalid_routine_agent_call_id");
});
it("blocks tool dispatch when the model stream is interrupted", async () => {
  fixture.mode = "interrupt";const execution = ports();
  expect(await runRoutineAgent(input(), execution)).toMatchObject({ state: "failed", code: "routine_agent_interrupted" });
  expect(execution.executeCapability).not.toHaveBeenCalled();
});
