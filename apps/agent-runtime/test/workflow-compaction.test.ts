import type { ModelMessage } from "ai";
import { beforeEach, expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({
  sent: [] as ModelMessage[][],
  summaries: [] as Array<{ older: number; window: number; sequence: number }>,
  inputTokens: 0,
  turns: 0,
}));
vi.mock("workflow", () => ({
  getWorkflowMetadata: () => ({ workflowRunId: "pinned-runtime" }),
  FatalError: class extends Error {}, RetryableError: class extends Error {},
  defineHook: () => ({}),
}));
vi.mock("../src/control-plane.js", () => ({
  controlPlaneRequest: async (_identity: unknown, operation: string) => {
    if (operation === "context") return { model_id: "fixture/model", system: "", prompt: "Read every page", allowed_tools: ["browser.read"], context_window_tokens: 100_000, model_turn_limit: 12 };
    if (operation === "steering") return { messages: [], closed: true };
    if (operation === "budget") return { version: 1, remaining_ms: 1800000, active: true, deadline: new Date(Date.now() + 1800000).toISOString() };
    return { accepted: true };
  },
}));
vi.mock("../src/mcp-runtime.js", () => ({
  discoverRemoteMCPTools: async () => ({ supported: true, tools: [{ name: "browser.read", description: "read", inputSchema: { type: "object", properties: {} } }] }),
}));
vi.mock("../src/compaction-step.js", () => ({
  summarizeOlderSteps: async (_identity: unknown, _model: string, older: ModelMessage[], window: number, sequence: number) => {
    fixture.summaries.push({ older: older.length, window, sequence });
    return "Done so far: read the first pages.";
  },
}));
vi.mock("@ai-sdk/workflow", () => ({
  WorkflowAgent: class {
    async stream({ messages }: { messages: ModelMessage[] }) {
      fixture.sent.push(messages);
      const n = ++fixture.turns;
      const done = n >= 8;
      const reply: ModelMessage[] = done
        ? [{ role: "assistant", content: [{ type: "text", text: "All pages read." }] }]
        : [
            { role: "assistant", content: [{ type: "tool-call", toolCallId: `c${n}`, toolName: "browser_read", input: { n } }] },
            { role: "tool", content: [{ type: "tool-result", toolCallId: `c${n}`, toolName: "browser_read", output: { type: "text", value: `page ${n} `.repeat(200) } }] },
          ];
      return {
        steps: [{ text: done ? "All pages read." : "", content: [], usage: { inputTokens: fixture.inputTokens } }],
        messages: [...messages, ...reply], finishReason: done ? "stop" : "tool-calls", toolResults: [],
        totalUsage: { inputTokens: fixture.inputTokens, outputTokens: 1 },
      };
    }
  },
}));
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
beforeEach(() => { fixture.sent = []; fixture.summaries = []; fixture.turns = 0; });

it("leaves a run alone while it fits comfortably in the window", async () => {
  fixture.inputTokens = 20_000;
  await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://api.test" });
  expect(fixture.summaries).toEqual([]);
  const last = JSON.stringify(fixture.sent.at(-1));
  expect(last).not.toContain("Earlier result cleared");
});

it("clears old results, then summarizes older steps once the model reports a nearly full window", async () => {
  fixture.inputTokens = 85_000;
  await runSpaceTaskAgent({ mistyRunId: "run-fixture", controlPlaneURL: "https://api.test" });
  expect(fixture.summaries.length).toBeGreaterThan(0);
  expect(fixture.summaries[0]).toMatchObject({ window: 100_000, sequence: 1 });
  const after = fixture.sent[fixture.sent.length - 1]!;
  const first = after[0]!;
  expect(first.role).toBe("user");
  expect(JSON.stringify(first.content)).toContain("Read every page");
  expect(JSON.stringify(first.content)).toContain("Done so far: read the first pages.");
  // Every kept tool result still follows the call it answers.
  const calls = after.flatMap((m) => (m.role === "assistant" && typeof m.content !== "string" ? m.content.filter((p) => p.type === "tool-call").map((p) => (p as { toolCallId: string }).toolCallId) : []));
  const results = after.flatMap((m) => (m.role === "tool" ? m.content.map((p) => (p as { toolCallId: string }).toolCallId) : []));
  expect(results.every((id) => calls.includes(id))).toBe(true);
  // Without compaction the last call would carry all 15 messages.
  expect(after.length).toBeLessThanOrEqual(8);
});
