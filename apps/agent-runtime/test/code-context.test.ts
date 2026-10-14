import type { ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { DROPPED_OUTPUT, DROPPED_READ, pruneStaleCodeResults } from "../src/code-context.js";

let id = 0;
function step(tool: string, input: Record<string, unknown>, output: Record<string, unknown>): ModelMessage[] {
  const toolCallId = `call_${++id}`;
  return [
    { role: "assistant", content: [{ type: "tool-call", toolCallId, toolName: tool, input }] },
    { role: "tool", content: [{ type: "tool-result", toolCallId, toolName: tool, output: { type: "text", value: JSON.stringify(output) } }] },
  ];
}

const big = (label: string) => `${label}\n${"x".repeat(50_000)}`;
const outputOf = (messages: ModelMessage[], index: number) => {
  const message = messages[index]!;
  if (message.role !== "tool") throw new Error("not a tool message");
  const part = message.content[0]!;
  if (part.type !== "tool-result" || part.output.type !== "text") throw new Error("not text");
  return JSON.parse(part.output.value) as Record<string, unknown>;
};

describe("pruneStaleCodeResults", () => {
  it("drops reads that a later edit or read superseded, once enough is stale", () => {
    const messages: ModelMessage[] = [
      { role: "user", content: "Build a site" },
      ...step("code_read", { path: "index.html" }, { path: "index.html", totalLines: 900, content: big("v1") }),
      ...step("code_edit", { path: "index.html", oldText: "a", newText: "b" }, { path: "index.html", around: "1:abc|b" }),
      ...step("code_read", { path: "./index.html" }, { path: "index.html", totalLines: 900, content: big("v2") }),
      ...step("code_read", { path: "style.css" }, { path: "style.css", totalLines: 10, content: big("css") }),
    ];
    const { messages: pruned, dropped } = pruneStaleCodeResults(messages);
    expect(dropped).toBe(1);
    expect(outputOf(pruned, 2).content).toBe(DROPPED_READ);
    expect(outputOf(pruned, 2).path).toBe("index.html");
    expect(outputOf(pruned, 6).content).toContain("v2");
    expect(outputOf(pruned, 8).content).toContain("css");
  });

  it("keeps the newest command output and the exit codes of older ones", () => {
    const messages: ModelMessage[] = [{ role: "user", content: "Build it" }];
    for (let index = 0; index < 5; index++) messages.push(...step("code_run", { command: "npm run build" }, { exitCode: index, output: big(`run ${index}`) }));
    const { messages: pruned, dropped } = pruneStaleCodeResults(messages);
    expect(dropped).toBe(2);
    expect(outputOf(pruned, 2)).toMatchObject({ exitCode: 0, output: DROPPED_OUTPUT });
    expect(outputOf(pruned, 4)).toMatchObject({ exitCode: 1, output: DROPPED_OUTPUT });
    expect(outputOf(pruned, 6).output).toContain("run 2");
  });

  it("leaves the transcript alone until a drop is worth a cache miss", () => {
    const messages: ModelMessage[] = [
      { role: "user", content: "Edit" },
      ...step("code_read", { path: "a.txt" }, { path: "a.txt", totalLines: 1, content: "small" }),
      ...step("code_read", { path: "a.txt" }, { path: "a.txt", totalLines: 1, content: "small" }),
    ];
    const result = pruneStaleCodeResults(messages);
    expect(result.dropped).toBe(0);
    expect(result.messages).toBe(messages);
  });

  it("never drops twice", () => {
    const messages: ModelMessage[] = [
      { role: "user", content: "Build a site" },
      ...step("code_read", { path: "a.html" }, { path: "a.html", totalLines: 9, content: big("one") }),
      ...step("code_read", { path: "a.html" }, { path: "a.html", totalLines: 9, content: big("two") }),
      ...step("code_read", { path: "a.html" }, { path: "a.html", totalLines: 9, content: big("three") }),
    ];
    const first = pruneStaleCodeResults(messages);
    expect(first.dropped).toBe(2);
    expect(pruneStaleCodeResults(first.messages).dropped).toBe(0);
  });
});
