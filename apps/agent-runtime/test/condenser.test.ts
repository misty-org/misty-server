import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { condense, summarizedSteps, type Condenser } from "../src/condenser.js";
import { toolState, toolUseTool } from "../src/toolsets.js";

const text = (content: string): ModelMessage => ({ role: "user", content });

describe("condense", () => {
  it("runs condensers in order, each on the previous one's result", async () => {
    const seen: string[] = [];
    const step = (name: string, keep: number): Condenser => ({
      name,
      async condense(messages, pressure) {
        seen.push(`${name}:${messages.length}`);
        return { messages: messages.slice(-keep), estimate: pressure.estimate / 2 };
      },
    });
    const result = await condense([text("a"), text("b"), text("c"), text("d")], { inputTokens: 0, sentBytes: 0, window: 1000 }, [step("first", 3), step("second", 1)]);
    expect(seen).toEqual(["first:4", "second:3"]);
    expect(result).toEqual([text("d")]);
  });

  it("leaves the transcript alone when a step has nothing to do", async () => {
    const untouched: Condenser = { name: "noop", condense: async () => undefined };
    const messages = [text("a")];
    expect(await condense(messages, { inputTokens: 0, sentBytes: 0, window: 1000 }, [untouched])).toBe(messages);
  });

  it("keeps the transcript when a summary fails", async () => {
    const failing = summarizedSteps(async () => {
      throw new Error("model unavailable");
    });
    const messages = Array.from({ length: 30 }, (_, index) => text(`message ${index} ${"x".repeat(4000)}`));
    expect(await condense(messages, { inputTokens: 0, sentBytes: 0, window: 1000 }, [failing])).toBe(messages);
  });
});

describe("toolState", () => {
  it("says whether the transcript still holds the calls that chose the toolset", () => {
    expect(toolState([], "answer").seen).toBe(false);
    const switched: ModelMessage = { role: "assistant", content: [{ type: "tool-call", toolCallId: "a", toolName: toolUseTool, input: { tool: "browse" } }] };
    expect(toolState([switched], "answer")).toMatchObject({ toolset: "browse", seen: true });
  });
});
