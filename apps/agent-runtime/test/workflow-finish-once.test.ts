import { beforeEach, expect, it, vi } from "vitest";

type Message = { role: string; content: unknown };
type FinishTool = { execute: (input: unknown, options: { toolCallId: string; messages: Message[] }) => Promise<unknown> };

const fixture = vi.hoisted(() => ({
  turn: 0,
  completions: [] as Array<Record<string, unknown>>,
  messageInputs: [] as Message[][],
  finishInputs: [] as unknown[],
}));

vi.mock("workflow", () => ({
  getWorkflowMetadata: () => ({ workflowRunId: "pinned-runtime" }),
  FatalError: class extends Error {},
  RetryableError: class extends Error {},
  defineHook: () => ({}),
}));
vi.mock("../src/control-plane.js", () => ({
  controlPlaneRequest: async (_identity: unknown, operation: string, body: Record<string, unknown>) => {
    if (operation === "context") return { model_id: "fixture/model", system: "", prompt: "What is the capital of France?", allowed_tools: [] };
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
const answer = "The capital of France is Paris.";
vi.mock("@ai-sdk/workflow", () => ({
  WorkflowAgent: class {
    constructor(private options: { tools: Record<string, FinishTool>; experimental_onStepStart: (step: { stepNumber: number }) => Promise<void> }) {}
    async stream({ messages }: { messages: Message[] }) {
      fixture.messageInputs.push(messages);
      await this.options.experimental_onStepStart({ stepNumber: 0 });
      const turn = fixture.turn++;
      const reply = (step: { text?: string; content: unknown[] }, response: Message, finishReason: string, toolResults: unknown[] = []) => ({
        steps: [step], toolResults, finishReason, totalUsage: { inputTokens: 10, outputTokens: 5 },
        messages: [...messages, response],
      });
      // Turn 1 looks something up, turn 2 answers in text, turn 3 confirms with agent_finish.
      if (turn === 0) {
        const call = { type: "tool-call", toolName: "agent_tool_load", toolCallId: "look", input: {} };
        return reply({ content: [call] }, { role: "assistant", content: [call] }, "tool-calls");
      }
      if (turn === 1) return reply({ text: answer, content: [{ type: "text", text: answer }] }, { role: "assistant", content: [{ type: "text", text: answer }] }, "stop");
      const input = { outcome: "completed", remaining: [] };
      fixture.finishInputs.push(input);
      const output = await this.options.tools.agent_finish!.execute(input, { toolCallId: "finish", messages });
      const call = { type: "tool-call", toolName: "agent_finish", toolCallId: "finish", input };
      return reply({ content: [call] }, { role: "assistant", content: [call] }, "tool-calls", [{ type: "tool-result", toolName: "agent_finish", toolCallId: "finish", output }]);
    }
  },
}));

import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
import { finishMissingNote } from "../src/task-completion.js";

beforeEach(() => {
  fixture.turn = 0;
  fixture.completions = [];
  fixture.messageInputs = [];
  fixture.finishInputs = [];
});

it("a text answer, the finish nudge, then the answer once", async () => {
  await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://control.invalid" });
  // The nudge asks for outcome and remaining only, and the model leaves summary out.
  expect(fixture.messageInputs[2]).toContainEqual({ role: "user", content: finishMissingNote });
  expect(fixture.finishInputs).toEqual([{ outcome: "completed", remaining: [] }]);
  // The text answer becomes the summary, so the final text is that answer once.
  expect(fixture.completions).toEqual([expect.objectContaining({ status: "success", text: answer })]);
});
