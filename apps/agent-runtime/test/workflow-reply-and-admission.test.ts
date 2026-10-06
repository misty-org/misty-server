import { beforeEach, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
  refuseAdmission: false,
  failSettlement: false,
  streams: 0,
  completions: [] as Array<Record<string, unknown>>,
  content: [] as Array<Record<string, unknown>>,
  text: "gmorning! hope your day's off to a good start.",
}));

vi.mock("workflow", () => ({
  getWorkflowMetadata: () => ({ workflowRunId: "pinned-runtime" }),
  FatalError: class extends Error {},
  RetryableError: class extends Error {},
  defineHook: () => ({}),
}));
vi.mock("../src/control-plane.js", () => ({
  controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, unknown>) => {
    if (operation === "context") return { model_id: "fixture/model", system: "", prompt: "gmorning", allowed_tools: [] };
    if (operation === "complete") fixture.completions.push(body);
    if (operation === "steering") return { messages: [], closed: true };
    if (operation === "budget") return { version: 1, remaining_ms: 1_800_000, active: true, deadline: new Date(Date.now() + 1_800_000).toISOString() };
    if (operation === "events" && String(body.node_id).startsWith("model:")) {
      if (body.state === "running" && fixture.refuseAdmission)
        throw new ControlPlaneError(402, "billing_admission_denied: Your account or command budget cannot cover this request.", "billing_admission_denied");
      if (body.state === "completed" && fixture.failSettlement)
        throw new ControlPlaneError(409, "billing_request_conflict: no matching hold", "billing_request_conflict");
    }
    return { accepted: true };
  },
}));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: [] }),
  requestMCPToolExecution: vi.fn(),
}));
vi.mock("@ai-sdk/workflow", () => ({
  WorkflowAgent: class {
    constructor(
      private callbacks: {
        experimental_onStepStart: (step: { stepNumber: number }) => Promise<void>;
        onStepEnd: (step: { finishReason: string; usage: unknown; text: string }) => Promise<void>;
      },
    ) {}
    async stream(options: { messages: unknown[] }) {
      fixture.streams++;
      await this.callbacks.experimental_onStepStart({ stepNumber: 0 });
      // The real agent swallows hook errors; so does this one.
      await this.callbacks.onStepEnd({ finishReason: "stop", usage: {}, text: fixture.text }).catch(() => {});
      return {
        steps: [{ text: fixture.text, content: fixture.content }],
        toolResults: [],
        messages: [...options.messages, { role: "assistant", content: fixture.text }],
        finishReason: "stop",
        totalUsage: { inputTokens: 10, outputTokens: 8 },
      };
    }
  },
}));

import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
import { ControlPlaneError } from "../src/control-plane-error.js";

beforeEach(() => {
  fixture.refuseAdmission = false;
  fixture.failSettlement = false;
  fixture.streams = 0;
  fixture.completions = [];
  fixture.content = [{ type: "text", text: fixture.text }];
});

const run = () => runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" }).catch(() => undefined);

it("treats a reply that took no actions as a finished answer, not an unconfirmed task", async () => {
  await run();
  expect(fixture.completions).toEqual([expect.objectContaining({ status: "success", text: fixture.text })]);
});

it("still requires a confirmed outcome once the run used a tool", async () => {
  fixture.content = [{ type: "tool-call", toolName: "notes.read", toolCallId: "read" }, { type: "text", text: fixture.text }];
  await run();
  expect(fixture.completions).toEqual([expect.objectContaining({ status: "incomplete", error_code: "task_outcome_unreported" })]);
});

it("stops before calling the model when billing refuses the turn, and says why", async () => {
  fixture.refuseAdmission = true;
  await run();
  expect(fixture.streams).toBe(0);
  expect(fixture.completions).toEqual([expect.objectContaining({ status: "failed", error_code: "billing_admission_denied" })]);
});

it("fails the run when the turn's usage cannot be settled, instead of carrying on unmetered", async () => {
  fixture.failSettlement = true;
  await run();
  expect(fixture.completions).toEqual([expect.objectContaining({ status: "failed" })]);
  expect(fixture.completions[0]?.text).toBe("");
});
