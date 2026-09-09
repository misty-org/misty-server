import { expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({ calls: [] as unknown[][], events: [] as unknown[], completions: [] as Record<string, unknown>[] }));
vi.mock("workflow", () => ({ getWorkflowMetadata: () => ({ workflowRunId: "routine-worker" }), FatalError: class extends Error {}, RetryableError: class extends Error {}, defineHook: () => ({}), sleep: vi.fn() }));
vi.mock("@ai-sdk/workflow", () => ({ WorkflowAgent: class { constructor() { throw new Error("A deterministic routine must not start the general agent"); } } }));
vi.mock("../src/control-plane.js", () => ({ controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, unknown>) => {
  if (operation === "context") return { allowed_tools: ["sdk.read", "sdk.save"], routine_execution: {
    routineId: "10000000-0000-4000-8000-000000000001", version: 1, runId: "run_routine", trigger: {},
    definition: { protocol: 1, name: "Daily habits", trigger: { kind: "manual" }, budget: {}, steps: ["read", "save"].map(id => ({ id, label: id, kind: "capability", action: { capability: "habits.list", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000002", targetRevision: 1 }, input: { kind: "literal", value: { content: "private habit data" } } })) },
    bindings: ["read", "save"].map((stepId, index) => ({ stepId, callId: `10000000-0000-4000-8000-00000000000${index + 3}`, toolName: `sdk.${stepId}` })),
  } };
  if (operation === "mcp-token") return {};
  if (operation === "budget") return { version: 1, active: true, remaining_ms: 1700000 };
  if (operation === "events") fixture.events.push(body);
  if (operation === "complete") fixture.completions.push(body);
  return { accepted: true };
} }));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: ["sdk.read", "sdk.save"].map(name => ({ name, description: name, inputSchema: { type: "object" } })) }),
  requestMCPToolExecution: async (...args: unknown[]) => { fixture.calls.push(args); return { result: { status: "success", result: { saved: true }, partial: false, evidence: [] } }; },
}));
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
it("routes a pinned routine through the existing durable capability path without model planning", async () => {
  const result = await runSpaceTaskAgent({ mistyRunId: "run_routine", controlPlaneURL: "https://control.invalid" });
  expect(result).toMatchObject({ state: "completed" });
  expect(fixture.calls).toHaveLength(2);
  expect(fixture.completions).toEqual([expect.objectContaining({ status: "success" })]);
  expect(JSON.stringify(fixture.events)).not.toContain("private habit data");
});
