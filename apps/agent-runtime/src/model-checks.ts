import { deflateSync } from "node:zlib";
import type { LanguageModelV4 } from "@ai-sdk/provider";
import { generateText, stepCountIs, tool, type ModelMessage } from "ai";
import { z } from "zod";

// Two quick checks that a model can be an agent here: it calls a tool and answers from the
// result, and it sees a screenshot a tool returned. Settings › Models runs them on the device's
// agent model (`/v1/device/model-test`); the free-model eval runs them before an Odysseys pass.

/** A solid-color PNG, so the model has one unambiguous thing to see. */
export function png(rgb: [number, number, number], size = 32): Buffer {
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
  const row = Buffer.concat([
    Buffer.from([0]),
    ...Array.from({ length: size }, () => Buffer.from(rgb)),
  ]);
  const pixels = deflateSync(Buffer.concat(Array.from({ length: size }, () => row)));
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", pixels),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

export interface CheckResult {
  ok: boolean;
  /** What the model did, for the person: never the model's raw output. */
  detail: string;
}

/** Calls a stock tool and answers with the number it returned (37). */
export async function toolCallCheck(
  model: LanguageModelV4,
  signal?: AbortSignal,
): Promise<CheckResult> {
  const result = await generateText({
    model,
    maxOutputTokens: 2_000,
    stopWhen: stepCountIs(4),
    maxRetries: 1,
    abortSignal: signal,
    tools: {
      stock: tool({
        description: "Look up how many units of a product a store has in stock.",
        inputSchema: z.object({ store: z.string(), product: z.string() }),
        execute: async ({ store }) => ({
          store,
          units: store.toLowerCase().includes("north") ? 37 : 0,
        }),
      }),
    },
    prompt:
      "How many units of the blue kettle does the North store have? Use the stock tool, then answer with the number.",
  });
  const called = result.steps
    .flatMap((step) => step.toolCalls)
    .some((call) => call.toolName === "stock");
  if (!called) return { ok: false, detail: "It answered without calling the tool." };
  if (!/37/.test(result.text))
    return { ok: false, detail: "It called the tool but didn't use the result." };
  return { ok: true, detail: "It called a tool and answered from the result." };
}

/** Reads the color of a screenshot returned by a tool (red). */
export async function imageCheck(
  model: LanguageModelV4,
  signal?: AbortSignal,
): Promise<CheckResult> {
  const messages: ModelMessage[] = [
    {
      role: "user",
      content: "Take a screenshot of the page, then tell me its single color in one word.",
    },
    {
      role: "assistant",
      content: [{ type: "tool-call", toolCallId: "shot-1", toolName: "browser_visual", input: {} }],
    },
    {
      role: "tool",
      content: [
        {
          type: "tool-result",
          toolCallId: "shot-1",
          toolName: "browser_visual",
          output: {
            type: "content",
            value: [
              { type: "text", text: '{"url":"https://example.test"}' },
              {
                type: "file",
                mediaType: "image/png",
                data: { type: "data", data: png([220, 20, 20]).toString("base64") },
              },
            ],
          },
        },
      ],
    },
  ];
  const result = await generateText({
    model,
    maxOutputTokens: 1_000,
    maxRetries: 1,
    abortSignal: signal,
    tools: {
      browser_visual: tool({ description: "Screenshot the page.", inputSchema: z.object({}) }),
    },
    toolChoice: "none",
    messages,
  });
  return /red/i.test(result.text)
    ? { ok: true, detail: "It saw a screenshot." }
    : { ok: false, detail: "It couldn't tell what the screenshot showed." };
}
