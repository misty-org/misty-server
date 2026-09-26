import { expect, it } from "vitest";
import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, simulateReadableStream, tool } from "ai";
import { MockLanguageModelV4 } from "ai/test";
import type { LanguageModelV4StreamPart } from "@ai-sdk/provider";
import { z } from "zod";

it("preserves a visual tool image when the foreground coordinator starts the next model turn", async () => {
  const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aOZkAAAAASUVORK5CYII=";
  let calls = 0;
  const model = new MockLanguageModelV4({ doStream: async () => {
    const visual = calls++ === 0;
    const chunks: LanguageModelV4StreamPart[] = [{ type: "stream-start", warnings: [] }];
    if (visual) chunks.push({ type: "tool-call", toolCallId: "visual-1", toolName: "visual", input: "{}" });
    chunks.push({ type: "finish", finishReason: { unified: visual ? "tool-calls" : "stop", raw: undefined }, usage: {
      inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
      outputTokens: { total: 5, text: 5, reasoning: 0 },
    } });
    return { stream: simulateReadableStream({ chunks, initialDelayInMs: 0, chunkDelayInMs: 0 }) };
  } });
  const agent = new WorkflowAgent({ model, stopWhen: isStepCount(1), tools: {
    visual: tool({ inputSchema: z.object({}), execute: async () => ({ documentId: "visual-document", image: { dataUrl: `data:image/png;base64,${png}` } }),
      toModelOutput: ({ output }) => ({ type: "content", value: [
        { type: "text", text: JSON.stringify({ documentId: output.documentId }) },
        { type: "image-data", data: png, mediaType: "image/png" },
      ] }),
    }),
  } });
  const first = await agent.stream({ prompt: "Inspect the screenshot", timeout: 30_000 });
  expect(first.finishReason).toBe("tool-calls");
  const second = await agent.stream({ messages: first.messages.filter(message => message.role !== "system"), timeout: 30_000 });
  expect(second.finishReason).toBe("stop");
  expect(model.doStreamCalls).toHaveLength(2);
  const toolMessage = model.doStreamCalls[1]!.prompt.find(message => message.role === "tool");
  expect(toolMessage).toBeDefined();
  const output = (toolMessage!.content[0] as any).output;
  expect(output.type).toBe("content");
  // The pinned V4 provider normalizes legacy image-data into a file part.
  expect(output.value).toContainEqual(expect.objectContaining({
    type: "file", data: { type: "data", data: png }, mediaType: "image/png",
  }));
});
