import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import {
  applyCompaction,
  CLEARED_RESULT,
  clearOldToolResults,
  contextWindow,
  DEFAULT_CONTEXT_WINDOW,
  estimateInputTokens,
  messageBytes,
  planCompaction,
  renderTranscript,
} from "../src/context-compaction.js";

function step(n: number, result = `page ${n} body `.repeat(20)): ModelMessage[] {
  return [
    { role: "assistant", content: [{ type: "tool-call", toolCallId: `call-${n}`, toolName: "browser_read", input: { page: n } }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId: `call-${n}`, toolName: "browser_read", output: { type: "text", value: result } }] },
  ];
}

const task: ModelMessage = { role: "user", content: "Compare the prices on these ten pages." };

describe("context compaction", () => {
  it("uses the model's window and falls back for unknown models", () => {
    expect(contextWindow(400_000)).toBe(400_000);
    expect(contextWindow(undefined)).toBe(DEFAULT_CONTEXT_WINDOW);
    expect(contextWindow(1_000)).toBe(16_000);
  });

  it("estimates from reported tokens plus what the transcript grew by", () => {
    const messages = [task, ...step(1)];
    const sent = messageBytes([task]);
    expect(estimateInputTokens(5_000, sent, messages)).toBe(5_000 + (messageBytes(messages) - sent) / 4);
  });

  it("clears all but the most recent tool results and keeps every call paired", () => {
    const messages = [task, ...Array.from({ length: 6 }, (_, n) => step(n)).flat()];
    const { messages: cleared, cleared: count } = clearOldToolResults(messages, 4);
    expect(count).toBe(2);
    const outputs = cleared.flatMap((message) => (message.role === "tool" ? message.content : []));
    expect(outputs.map((part) => part.type === "tool-result" && part.output.type === "text" && part.output.value === CLEARED_RESULT)).toEqual([
      true, true, false, false, false, false,
    ]);
    // Calls and results still pair one to one.
    expect(cleared.filter((message) => message.role === "tool")).toHaveLength(6);
    expect(cleared.filter((message) => message.role === "assistant")).toHaveLength(6);
    expect(messageBytes(cleared)).toBeLessThan(messageBytes(messages));
    // Clearing again changes nothing.
    expect(clearOldToolResults(cleared, 4).cleared).toBe(0);
  });

  it("keeps errors but shortens long ones", () => {
    const messages: ModelMessage[] = [
      task,
      ...step(1),
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "e", toolName: "notes_update", input: {} }] },
      { role: "tool", content: [{ type: "tool-result", toolCallId: "e", toolName: "notes_update", output: { type: "error-text", value: "x".repeat(5_000) } }] },
      ...step(2),
    ];
    const { messages: cleared } = clearOldToolResults(messages, 1);
    const error = cleared[4]!.role === "tool" ? cleared[4]!.content[0] : undefined;
    expect(error?.type === "tool-result" && error.output.type === "error-text" && error.output.value.length).toBe(2_000);
  });

  it("summarizes older steps but keeps the task, the person's words and a tail that starts with the model", () => {
    const messages = [
      task,
      ...step(1),
      { role: "user", content: "Skip the second store." } as ModelMessage,
      ...step(2),
      ...step(3),
      ...step(4),
      ...step(5),
    ];
    const plan = planCompaction(messages, 5)!;
    expect(plan.task).toBe(task);
    expect(plan.personMessages).toEqual(["Skip the second store."]);
    expect(plan.recent[0]!.role).toBe("assistant");
    expect(plan.older.length + plan.recent.length + 1).toBe(messages.length);
    const compacted = applyCompaction(plan, "Task: compare prices.\nDone so far: read pages 1-2.");
    expect(compacted[0]!.role).toBe("user");
    const text = typeof compacted[0]!.content === "string" ? compacted[0]!.content : "";
    expect(text).toContain("Compare the prices on these ten pages.");
    expect(text).toContain("Done so far: read pages 1-2.");
    expect(text).toContain("- Skip the second store.");
    expect(text).toContain("untrusted data");
    expect(compacted.slice(1)).toEqual(plan.recent);
  });

  it("does nothing when there is too little history", () => {
    expect(planCompaction([task, ...step(1)], 6)).toBeNull();
  });

  it("renders the older steps as plain text within a limit, keeping the newest", () => {
    const transcript = renderTranscript([...step(1, "first result"), { role: "user", content: "Use euros." }, ...step(2, "second result")], 10_000);
    expect(transcript).toContain('Agent called browser_read with {"page":1}');
    expect(transcript).toContain("Result of browser_read: first result");
    expect(transcript).toContain("Person: Use euros.");
    const clipped = renderTranscript([...step(1, "old ".repeat(500)), ...step(2, "newest result")], 200);
    expect(clipped.startsWith("[Earliest steps omitted]")).toBe(true);
    expect(clipped).toContain("newest result");
  });
});
