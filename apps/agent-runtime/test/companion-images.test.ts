import { createHash } from "node:crypto";
import { expect, it } from "vitest";
import { WorkflowAgent } from "@ai-sdk/workflow";
import { simulateReadableStream } from "ai";
import { MockLanguageModelV4 } from "ai/test";
import { companionImageParts } from "../src/companion-images.js";

it("delivers the same decoded bytes and labeled coordinate space to the pinned model API", async () => {
  const png =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aOZkAAAAASUVORK5CYII=";
  const bytes = Buffer.from(png, "base64");
  const hash = createHash("sha256").update(bytes).digest("hex");
  const capturedAt = Date.UTC(2026, 8, 27);
  const parts = companionImageParts([
    {
      id: "fixture",
      name: "synthetic",
      screen: "screen1",
      primary: true,
      width: 1,
      height: 1,
      mime_type: "image/png",
      data_url: `data:image/png;base64,${png}`,
      content_hash: hash,
      captured_at: capturedAt,
      display_id: 7,
    },
  ]);
  const model = new MockLanguageModelV4({
    doStream: async () => ({
      stream: simulateReadableStream({
        initialDelayInMs: 0,
        chunkDelayInMs: 0,
        chunks: [
          { type: "stream-start", warnings: [] },
          { type: "text-start", id: "text" },
          { type: "text-delta", id: "text", delta: "A synthetic pixel." },
          { type: "text-end", id: "text" },
          {
            type: "finish",
            finishReason: { unified: "stop", raw: undefined },
            usage: {
              inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
              outputTokens: { total: 5, text: 5, reasoning: 0 },
            },
          },
        ],
      }),
    }),
  });
  const agent = new WorkflowAgent({ model, tools: {} });
  await agent.stream({ messages: [{ role: "user", content: parts }], timeout: 30000 });
  const message = model.doStreamCalls[0]!.prompt.find((message) => message.role === "user")!;
  const text = message.content.find((part) => part.type === "text");
  expect(text).toMatchObject({ text: expect.stringContaining(hash) });
  expect(text).toMatchObject({ text: expect.stringContaining("1 x 1 screenshot pixels") });
  expect(text).toMatchObject({ text: expect.stringContaining("2026-09-27T00:00:00.000Z") });
  const image = message.content.find((part) => part.type === "file")!;
  expect(image).toMatchObject({ mediaType: "image/png" });
  expect(image.data.type).toBe("data");
  if (image.data.type !== "data") throw new Error("Expected inline image bytes");
  const modelBytes =
    typeof image.data.data === "string"
      ? Buffer.from(image.data.data, "base64")
      : Buffer.from(image.data.data);
  expect(modelBytes).toEqual(bytes);
  expect(model.doStreamCalls).toHaveLength(1);
});
