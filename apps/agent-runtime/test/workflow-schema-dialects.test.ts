import { readFileSync } from "node:fs";
import { expect, it, vi } from "vitest";
import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, jsonSchema, simulateReadableStream, tool } from "ai";
import { MockLanguageModelV4 } from "ai/test";
import type { LanguageModelV4StreamPart } from "@ai-sdk/provider";
import { z } from "zod";

const canonical = JSON.parse(readFileSync(new URL("../../../internal/capabilities/builtins.json", import.meta.url), "utf8")) as Array<{ name: string; inputSchema: any }>;
const usage = { inputTokens: { total: 1, noCache: 1, cacheRead: 0, cacheWrite: 0 }, outputTokens: { total: 1, text: 1, reasoning: 0 } };

function model(call?: { toolName: string; input: string }) {
  return new MockLanguageModelV4({ doStream: async () => {
    const chunks: LanguageModelV4StreamPart[] = [{ type: "stream-start", warnings: [] }];
    if (call) chunks.push({ type: "tool-call", toolCallId: "pilot-schema-check", ...call });
    else chunks.push({ type: "text-start", id: "ready" }, { type: "text-delta", id: "ready", delta: "Ready" }, { type: "text-end", id: "ready" });
    chunks.push({ type: "finish", finishReason: { unified: call ? "tool-calls" : "stop", raw: undefined }, usage });
    return { stream: simulateReadableStream({ chunks, initialDelayInMs: 0, chunkDelayInMs: 0 }) };
  } });
}

it("the actual pinned workflow adapter reconstructs every canonical SDK schema alongside Zod tools", async () => {
  const agent = new WorkflowAgent({ model: model(), stopWhen: isStepCount(1), tools: {
    ...Object.fromEntries(canonical.map((definition, i) => [`capability_${i}`, tool({ inputSchema: jsonSchema(definition.inputSchema), execute: async () => ({}) })])),
    discover: tool({ inputSchema: z.object({ query: z.string() }), execute: async () => ({}) }),
  } });
  const result = await agent.stream({ prompt: "Readiness check" });
  expect(result.steps[0]?.text).toBe("Ready");
});

for (const input of [
  { id: "10000000-0000-4000-8000-000000000001", date: "2026-09-08", extra: "forbidden" },
  { id: "not-a-uuid", date: "2026-09-08" },
  { id: "10000000-0000-4000-8000-000000000001", date: "2026-02-30" },
  { id: "10000000-0000-4000-8000-000000000001", date: "2026-09-08" },
]) {
  it(`preserves validation across workflow serialization: ${JSON.stringify(input)}`, async () => {
    const execute = vi.fn(async () => ({ verified: true }));
    const agent = new WorkflowAgent({ model: model({ toolName: "verified_action", input: JSON.stringify(input) }), stopWhen: isStepCount(1), tools: {
      verified_action: tool({ inputSchema: jsonSchema({
        $schema: "https://json-schema.org/draft/2020-12/schema", type: "object",
        properties: { id: { type: "string", format: "uuid" }, date: { type: "string", format: "date" } },
        required: ["id", "date"], additionalProperties: false,
      }), execute }),
    } });
    await agent.stream({ prompt: "Validate a controlled test action" });
    expect(execute).toHaveBeenCalledTimes(input.id.startsWith("1000") && input.date === "2026-09-08" && !("extra" in input) ? 1 : 0);
  });
}
