// Whether a free eval model (docs/agents/evals/free-model.md) can do what an Odysseys pass asks of
// it, in about a dozen requests: call tools and read their results, see a screenshot that a tool
// returned, accept the agent's 32,000-token output cap, and take a judge's many screenshots in
// one message. Run before a pass:
//   NVIDIA_API_KEY=… npm run eval:free-model   (MISTY_FREE_MODEL=nim/… checks another model)
import { deflateSync } from "node:zlib";
import { generateText, stepCountIs, tool, type ModelMessage } from "ai";
import { describe, expect, it } from "vitest";
import { z } from "zod";
import { RoutedModel } from "../../src/model-calls.js";

const modelId = process.env.MISTY_FREE_MODEL?.trim() || "nim/z-ai/glm-5.3-flash";
const ready = Boolean(process.env.NVIDIA_API_KEY?.trim()) && /^(nim|nvidia)\//.test(modelId);
const model = () => new RoutedModel(modelId, { provider: "instance" });

/** A solid-color PNG, so the model has one unambiguous thing to see. */
function png(rgb: [number, number, number], size = 32): Buffer {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (bytes: Buffer) => {
    let c = 0xffffffff;
    for (const byte of bytes) c = crcTable[(c ^ byte) & 0xff]! ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type: string, data: Buffer) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const out = Buffer.alloc(body.length + 8);
    out.writeUInt32BE(data.length, 0);
    body.copy(out, 4);
    out.writeUInt32BE(crc(body), body.length + 4);
    return out;
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header.set([8, 2, 0, 0, 0], 8);
  const row = Buffer.concat([Buffer.from([0]), ...Array.from({ length: size }, () => Buffer.from(rgb))]);
  const pixels = deflateSync(Buffer.concat(Array.from({ length: size }, () => row)));
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", header), chunk("IDAT", pixels), chunk("IEND", Buffer.alloc(0))]);
}

describe.skipIf(!ready)(`free model ${modelId}`, () => {
  it("calls a tool, reads its result and answers from it", async () => {
    const result = await generateText({
      model: model(),
      maxOutputTokens: 2_000,
      stopWhen: stepCountIs(4),
      tools: {
        stock: tool({
          description: "Look up how many units of a product a store has in stock.",
          inputSchema: z.object({ store: z.string(), product: z.string() }),
          execute: async ({ store }) => ({ store, units: store.toLowerCase().includes("north") ? 37 : 0 }),
        }),
      },
      prompt: "How many units of the blue kettle does the North store have? Use the stock tool, then answer with the number.",
    });
    expect(result.steps.flatMap((step) => step.toolCalls).map((call) => call.toolName)).toContain("stock");
    expect(result.text).toMatch(/37/);
  });

  it("sees a screenshot a tool returned", async () => {
    const messages: ModelMessage[] = [
      { role: "user", content: "Take a screenshot of the page, then tell me its single color in one word." },
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "shot-1", toolName: "browser_visual", input: {} }] },
      {
        role: "tool",
        content: [{
          type: "tool-result", toolCallId: "shot-1", toolName: "browser_visual",
          output: { type: "content", value: [{ type: "text", text: "{\"url\":\"https://example.test\"}" }, { type: "image-data", data: png([220, 20, 20]).toString("base64"), mediaType: "image/png" }] },
        }],
      },
    ];
    const result = await generateText({
      model: model(),
      maxOutputTokens: 1_000,
      tools: { browser_visual: tool({ description: "Screenshot the page.", inputSchema: z.object({}) }) },
      toolChoice: "none",
      messages,
    });
    expect(result.text.toLowerCase()).toMatch(/red/);
  });

  it("accepts the agent's 32,000-token output cap", async () => {
    const result = await generateText({ model: model(), maxOutputTokens: 32_000, prompt: "Reply with the word ready." });
    expect(result.text.toLowerCase()).toMatch(/ready/);
  });

  it("takes a judge's message with many screenshots", async () => {
    const colors: Array<[number, number, number]> = Array.from({ length: 20 }, (_, index) => (index === 19 ? [20, 20, 220] : [235, 235, 235]));
    const result = await generateText({
      model: model(),
      maxOutputTokens: 1_000,
      messages: [{
        role: "user",
        content: [
          { type: "text", text: `${colors.length} screenshots follow. What is the color of the last one? One word.` },
          ...colors.map((rgb) => ({ type: "file" as const, mediaType: "image/png", data: png(rgb) })),
        ],
      }],
    });
    expect(result.text.toLowerCase()).toMatch(/blue/);
  });
});
