import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
  aborted: false,
  finishReason: "stop",
  steps: 1,
  completions: [] as Array<Record<string, unknown>>,
  budgetVersion: 1,
  budgetReads: 0,
  timeoutValues: [] as number[],
  modelNodes: [] as string[],
  messageInputs: [] as unknown[][],
  outcomes: [] as string[],
  advanceAfterFirst: 0,
  budgetFailureAfterFirst: false,
}));

vi.mock("workflow", () => ({
  getWorkflowMetadata: () => ({ workflowRunId: "pinned-runtime" }),
  FatalError: class extends Error {},
  RetryableError: class extends Error {},
  defineHook: () => ({}),
}));
vi.mock("../src/control-plane.js", () => ({
  controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, unknown>) => {
    if (operation === "context") return {
      model_id: "fixture/model", system: "", prompt: "Summarize this request",
      allowed_tools: [], required_tools: [],
    };
    if (operation === "complete") fixture.completions.push(body);
    if (operation === "budget") {
      fixture.budgetReads++;
      if (fixture.budgetFailureAfterFirst && fixture.budgetReads>1) throw new ControlPlaneError(422, "agent_execution_time_limit", "agent_execution_time_limit");
      const remaining = fixture.budgetReads === 1 ? 1_800_000 : 1_200_000;
      return { version: fixture.budgetVersion, remaining_ms: remaining, active: true, deadline: new Date(Date.now()+remaining).toISOString() };
    }
    if (operation === "events" && body.state === "running" && String(body.node_id).startsWith("model:")) fixture.modelNodes.push(String(body.node_id));
    return { accepted: true };
  },
}));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: [] }),
  requestMCPToolExecution: vi.fn(),
}));
vi.mock("@ai-sdk/workflow", () => ({
  WorkflowAgent: class {
    constructor(private callbacks: { experimental_onStepStart: (step: { stepNumber: number }) => Promise<void> }) {}
    async stream(options: { onAbort?: () => Promise<void>; timeout: number; messages: unknown[] }) {
      fixture.timeoutValues.push(options.timeout);
      fixture.messageInputs.push(options.messages);
      await this.callbacks.experimental_onStepStart({ stepNumber: 0 });
      if (fixture.aborted) await options.onAbort?.();
      if (fixture.advanceAfterFirst && fixture.timeoutValues.length === 1) vi.spyOn(Date, "now").mockReturnValue(Date.now()+fixture.advanceAfterFirst);
      return {
        steps: Array.from({ length: fixture.steps }, () => ({
          text: "Earlier model text that must not prove completion.",
          content: [{ type: "text", text: "Earlier model text that must not prove completion." }],
        })),
        messages: [{ role: "system", content: "adapter instructions" }, ...options.messages, { role: "assistant", content: "Prior model turn" }, { role: "tool", content: [{ type: "tool-result", toolCallId: "prior-call", toolName: "notes.read", output: { type: "text", value: "Observed note" } }] }],
        finishReason: fixture.outcomes.shift() ?? fixture.finishReason,
        totalUsage: { inputTokens: 100, outputTokens: 12 },
      };
    }
  },
}));

import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
import { ControlPlaneError } from "../src/control-plane-error.js";

beforeEach(() => {
  fixture.aborted = false;
  fixture.finishReason = "stop";
  fixture.steps = 1;
  fixture.completions = [];
  fixture.budgetVersion=1; fixture.budgetReads=0; fixture.timeoutValues=[]; fixture.messageInputs=[];
  fixture.modelNodes=[]; fixture.outcomes=[]; fixture.advanceAfterFirst=0; fixture.budgetFailureAfterFirst=false;
});
afterEach(() => vi.restoreAllMocks());

describe("workflow completion evidence", () => {
  it("does not publish success when an aborted stream resolves with prior text and a stop reason", async () => {
    fixture.aborted = true;
    const result = await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
    expect(result).toMatchObject({ incomplete: true });
    expect(fixture.completions).toEqual([expect.objectContaining({
      status: "incomplete", error_code: "agent_runtime_interrupted",
      usage: { inputTokens: 100, outputTokens: 12 },
    })]);
  });

  it.each(["length", "content-filter", "error", "other"])("does not publish unfinished %s results as success", async (reason) => {
    fixture.finishReason = reason;
    await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
    expect(fixture.completions).toEqual([expect.objectContaining({
      status: "incomplete", error_code: "agent_response_incomplete",
    })]);
  });

  it("retains the durable turn-limit outcome when the local stop fires", async () => {
    fixture.finishReason = "tool-calls";
    await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "incomplete", error_code: "agent_model_turn_limit" })]);
    expect(fixture.timeoutValues).toHaveLength(20);
    expect(fixture.modelNodes).toEqual(Array.from({ length: 20 }, (_, index) => `model:${index+1}`));
  });

  it("accepts a normally finished answer", async () => {
    await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "success" })]);
  });

  it("requires a completed step even if a transport claims stop", async () => {
    fixture.steps = 0;
    await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "incomplete" })]);
  });

  it("refreshes the active-time deadline after a six-hour durable wait and carries the tool transcript", async () => {
    fixture.outcomes=["tool-calls","stop"];
    fixture.advanceAfterFirst=6*60*60_000;
    await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
    expect(fixture.timeoutValues[0]).toBeGreaterThan(1_799_000);
    expect(fixture.timeoutValues[1]).toBeGreaterThan(1_199_000);
    expect(fixture.modelNodes).toEqual(["model:1","model:2"]);
    expect(fixture.messageInputs[1]).toEqual(expect.arrayContaining([expect.objectContaining({ role: "tool" })]));
    expect(fixture.messageInputs[1]).not.toEqual(expect.arrayContaining([expect.objectContaining({ role: "system" })]));
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "success", usage: expect.objectContaining({ inputTokens: 200, outputTokens: 24 }) })]);
  });

  it("stops before the next model when the control plane reports exhaustion, retaining earlier usage", async () => {
    fixture.outcomes=["tool-calls"];
    fixture.budgetFailureAfterFirst=true;
    await expect(runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" })).rejects.toThrow("execution-time allowance");
    expect(fixture.timeoutValues).toHaveLength(1);
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "failed", error_code: "agent_execution_time_limit", usage: { inputTokens: 100, outputTokens: 12 } })]);
  });

  it("retains the absolute deadline policy for older admissions", async () => {
    fixture.budgetVersion=0; fixture.outcomes=["tool-calls"]; fixture.advanceAfterFirst=6*60*60_000;
    await expect(runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" })).rejects.toThrow("timed out");
    expect(fixture.timeoutValues).toHaveLength(1);
    expect(fixture.completions).toEqual([expect.objectContaining({ status: "failed", error_code: "agent_runtime_timeout" })]);
  });
});
