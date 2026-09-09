import { beforeEach, expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({ phases: [] as string[], awake: false, interrupted: false, calls: 0, expiresAt: new Date(Date.now() + 86400000).toISOString(), until: new Date(Date.now() + 3600000).toISOString(), waitId: "10000000-0000-4000-8000-000000000004" }));
vi.mock("workflow", () => ({ getWorkflowMetadata: () => ({ workflowRunId: "timer-worker" }), FatalError: class extends Error {}, RetryableError: class extends Error {}, defineHook: () => ({}), sleep: async (milliseconds: number) => {
  expect(milliseconds).toBe(1000);fixture.phases.push("sleep");if (fixture.interrupted) throw new Error("cancelled");
} }));
vi.mock("@ai-sdk/workflow", () => ({ WorkflowAgent: class { constructor() { throw new Error("Timed routines must not allocate a model loop"); } } }));
vi.mock("../src/control-plane.js", () => ({ controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, any>) => {
  if (operation === "context") {
    expect(body.routine_wait_protocol).toBe(1);
    return { allowed_tools: ["sdk.save"], routine_execution: {
      routineId: "10000000-0000-4000-8000-000000000001", version: 1, runId: "run_timer", trigger: {},
      definition: { protocol: 1, name: "Wait then save", trigger: { kind: "manual" }, budget: {}, steps: [
        { id: "pause", kind: "wait", label: "Wait", until: { kind: "literal", value: fixture.until } },
        { id: "save", kind: "capability", label: "Save", action: { capability: "habits.record", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000002", targetRevision: 1 }, input: { kind: "reference", source: { kind: "step", stepId: "pause" }, path: [] } },
      ] }, bindings: [{ stepId: "save", callId: "10000000-0000-4000-8000-000000000003", toolName: "sdk.save" }],
    } };
  }
  if (operation === "routine-wait") {
    fixture.phases.push(body.phase);
    if (body.phase === "resume") {expect(body.wait_id).toBe(fixture.waitId);fixture.awake = true;}
    return { stepId: "pause", waitId: fixture.waitId, state: fixture.awake ? "completed" : "waiting", until: fixture.until, expiresAt: fixture.expiresAt, remainingMs: fixture.awake ? 0 : 1000 };
  }
  if (operation === "budget") return { version: 1, active: true, remaining_ms: 1700000 };
  return { accepted: true };
} }));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: [{ name: "sdk.save", description: "Save", inputSchema: { type: "object" } }] }),
  requestMCPToolExecution: async () => {expect(fixture.awake).toBe(true);fixture.calls++;return { result: { status: "success", result: {}, partial: false, evidence: [] } };},
}));
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
beforeEach(() => { fixture.phases = []; fixture.awake = false; fixture.interrupted = false; fixture.calls = 0; });
it("wires timed waits through the durable worker before dependent effects", async () => {
  expect(await runSpaceTaskAgent({ mistyRunId: "run_timer", controlPlaneURL: "https://control.invalid" })).toMatchObject({ state: "completed" });
  expect(fixture.phases).toEqual(["open", "sleep", "resume"]);expect(fixture.calls).toBe(1);
});
it("does not dispatch a dependent action after sleep is interrupted", async () => {
  fixture.interrupted = true;
  expect(await runSpaceTaskAgent({ mistyRunId: "run_timer", controlPlaneURL: "https://control.invalid" })).toMatchObject({ state: "uncertain" });
  expect(fixture.calls).toBe(0);expect(fixture.phases).toEqual(["open", "sleep"]);
});
