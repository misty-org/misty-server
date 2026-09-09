import { expect, it, vi } from "vitest";
const model = vi.hoisted(() => ({ run: vi.fn(async () => ({ state: "completed", output: { summary: "private summary" }, modelTurns: 1, capabilityCalls: 0 })) }));
vi.mock("../src/routine-agent.js", () => ({ runRoutineAgent: model.run }));
import { routineAgentAdapter, type RoutineAgentControl } from "../src/routine-agent-adapter.js";
const pin = { capability: "habits.list", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000001", targetRevision: 1 };
const step = { id: "summarize", kind: "agent" as const, label: "Summarize", prompt: { kind: "literal" as const, value: "Summarize" }, actions: [pin], maxTurns: 2, outputSchema: { type: "object" } };
const binding = { stepId: step.id, callNamespace: "10000000-0000-4000-8000-000000000002", tools: [{ action: pin, toolName: "sdk.read" }] };
const outcome = { status: "success", result: { summary: "private summary" }, partial: false, evidence: [] };
const control = (): RoutineAgentControl => ({
  open: vi.fn(async () => ({ state: "running" as const, protocol: 1 as const, stepId: step.id, callNamespace: binding.callNamespace, prompt: "Summarize", modelId: "model", maxTurns: 2 })),
  finish: vi.fn(async () => outcome), beginModel: vi.fn(), finishModel: vi.fn(), executeCapability: vi.fn(),
});
it("replays a confirmed Go checkpoint without invoking a model", async () => {
  model.run.mockClear();const port = control();vi.mocked(port.open).mockResolvedValue({ state: "replayed", outcome });
  expect(await routineAgentAdapter([], port)(step, "Summarize", binding)).toEqual(outcome);
  expect(model.run).not.toHaveBeenCalled();expect(port.finish).not.toHaveBeenCalled();
});
it("requires admission prompt agreement before any model work", async () => {
  model.run.mockClear();await expect(routineAgentAdapter([], control())(step, "A changed prompt", binding)).rejects.toThrow("routine_agent_admission_mismatch");
  expect(model.run).not.toHaveBeenCalled();
});
it("requires a valid Go completion acknowledgement after model output", async () => {
  const port = control();vi.mocked(port.finish).mockResolvedValue(undefined);
  await expect(routineAgentAdapter([], port)(step, "Summarize", binding)).rejects.toThrow();
  expect(port.finish).toHaveBeenCalledWith(step.id, expect.objectContaining({ output: { summary: "private summary" } }));
});
