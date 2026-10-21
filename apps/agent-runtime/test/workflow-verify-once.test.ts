import { beforeEach, expect, it, vi } from "vitest";

type Message = { role: string; content: unknown };
type FinishTool = { execute: (input: unknown, options: { toolCallId: string; messages: Message[] }) => Promise<unknown> };

const fixture = vi.hoisted(() => ({
  prompt: "",
  turn: 0,
  completions: [] as Array<Record<string, unknown>>,
  finishOutputs: [] as unknown[],
}));

vi.mock("workflow", () => ({
  getWorkflowMetadata: () => ({ workflowRunId: "pinned-runtime" }),
  FatalError: class extends Error {},
  RetryableError: class extends Error {},
  defineHook: () => ({}),
}));
vi.mock("../src/control-plane.js", () => ({
  controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, unknown>) => {
    if (operation === "context") return { model_id: "fixture/model", system: "", prompt: fixture.prompt, allowed_tools: [] };
    if (operation === "complete") fixture.completions.push(body);
    if (operation === "steering") return { messages: [], closed: true };
    if (operation === "budget") return { version: 1, remaining_ms: 1_800_000, active: true, deadline: new Date(Date.now() + 1_800_000).toISOString() };
    return { accepted: true };
  },
}));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: [] }),
  requestMCPToolExecution: vi.fn(),
}));
// The long request's ledger and review are model calls of their own; neither finds anything here.
vi.mock("../src/requirements-review.js", async (original) => ({
  ...(await original<typeof import("../src/requirements-review.js")>()),
  requirementsLedger: async () => "",
  reviewAnswer: async () => [],
}));
const partial = {
  outcome: "partial",
  summary: "Palace's Winter drop began October 4. A Jordan 11 Low is listed for October 23, but I couldn't confirm the date.",
  remaining: [{ requirement: "Confirm the Jordan 11 Low release date", reason: "the article doesn't give it" }],
};
vi.mock("@ai-sdk/workflow", () => ({
  WorkflowAgent: class {
    constructor(private options: { tools: Record<string, FinishTool>; experimental_onStepStart: (step: { stepNumber: number }) => Promise<void> }) {}
    async stream({ messages }: { messages: Message[] }) {
      await this.options.experimental_onStepStart({ stepNumber: 0 });
      const turn = fixture.turn++;
      const reply = (content: unknown[], finishReason: string, toolResults: unknown[] = []) => ({
        steps: [{ content }], toolResults, finishReason, totalUsage: { inputTokens: 10, outputTokens: 5 },
        messages: [...messages, { role: "assistant", content }],
      });
      // Every other turn looks something up; the turns between report the same partial answer.
      if (turn % 2 === 0) return reply([{ type: "tool-call", toolName: "agent_tool_load", toolCallId: `look-${turn}`, input: {} }], "tool-calls");
      const output = await this.options.tools.agent_finish!.execute(partial, { toolCallId: `finish-${turn}`, messages });
      fixture.finishOutputs.push(output);
      return reply([{ type: "tool-call", toolName: "agent_finish", toolCallId: `finish-${turn}`, input: partial }], "tool-calls",
        [{ type: "tool-result", toolName: "agent_finish", toolCallId: `finish-${turn}`, output }]);
    }
  },
}));

import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";

beforeEach(() => {
  fixture.turn = 0;
  fixture.completions = [];
  fixture.finishOutputs = [];
});

it("a short question's honest partial answer stands the first time", async () => {
  fixture.prompt = "What's dropping in streetwear this week?";
  await runSpaceTaskAgent({ mistyRunId: "run-short", controlPlaneURL: "https://control.invalid" });
  expect(fixture.finishOutputs).toEqual([expect.objectContaining({ outcome: "partial" })]);
  expect(fixture.completions).toEqual([expect.objectContaining({ error_code: "task_incomplete" })]);
});

it("a long request's give-up goes back, and a next finish that found nothing new stands", async () => {
  fixture.prompt = `Find every streetwear release this week. ${"For each one give the brand, item, price, date and the store, with a link to the page you read. ".repeat(4)}`;
  await runSpaceTaskAgent({ mistyRunId: "run-long", controlPlaneURL: "https://control.invalid" });
  expect(fixture.finishOutputs).toHaveLength(2);
  expect(fixture.finishOutputs[0]).toEqual({ status: "failure", tool_error: { message: expect.stringMatching(/Confirm the Jordan 11 Low release date[\s\S]*Keep working on the open parts/) } });
  expect(fixture.finishOutputs[1]).toEqual(expect.objectContaining({ outcome: "partial" }));
});
