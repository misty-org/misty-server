import { afterEach, expect, it, vi } from "vitest";
import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, simulateReadableStream, tool } from "ai";
import { MockLanguageModelV4 } from "ai/test";
import type { LanguageModelV4StreamPart } from "@ai-sdk/provider";
import { z } from "zod";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";

afterEach(() => vi.restoreAllMocks());
const usage = { inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 }, outputTokens: { total: 5, text: 5, reasoning: 0 } };

it("the pinned adapter returns after one tool turn and accepts its full transcript after a long wait", async () => {
  let calls=0;
  const model = new MockLanguageModelV4({ doStream: async () => {
    calls++;
    const chunks: LanguageModelV4StreamPart[] = [{ type: "stream-start", warnings: [] }];
    if (calls===1) chunks.push({ type: "tool-call", toolCallId: "read-1", toolName: "read_note", input: "{}" });
    else chunks.push({ type: "text-start", id: "answer" }, { type: "text-delta", id: "answer", delta: "The observed note says walked." }, { type: "text-end", id: "answer" });
    chunks.push({ type: "finish", finishReason: { unified: calls===1 ? "tool-calls" : "stop", raw: undefined }, usage });
    return { stream: simulateReadableStream({ chunks, initialDelayInMs: 0, chunkDelayInMs: 0 }) };
  } });
  const agent = new WorkflowAgent({ model, instructions: "Summarize the note.", stopWhen: isStepCount(1), tools: {
    read_note: tool({ inputSchema: z.object({}), execute: async () => {
      // The workflow's durable wait advances its clock without keeping a model
      // request active. No real service or six-hour wall-clock delay is needed.
      vi.spyOn(Date,"now").mockReturnValue(Date.now()+6*60*60_000);
      // Also let the real model AbortSignal expire while only the tool is
      // waiting. That expired signal must not turn the finished model into an abort.
      await new Promise(resolve => setTimeout(resolve,150));
      return { text: "walked" };
    } }),
  } });
  let aborted=false;
  const first = await agent.stream({ prompt: "Read my note", timeout: 100, onAbort: async () => { aborted=true; } });
  expect(calls).toBe(1);
  expect(aborted).toBe(false);
  expect(first.finishReason).toBe("tool-calls");
  expect(first.steps).toHaveLength(1);
  const second = await agent.stream({ messages: first.messages.filter(message => message.role!=="system"), timeout: 30_000 });
  expect(second.finishReason).toBe("stop");
  expect(second.steps[0]?.text).toContain("walked");
  expect(model.doStreamCalls[1]?.prompt.filter(message => message.role==="system")).toHaveLength(1);
  expect(JSON.stringify(model.doStreamCalls[1]?.prompt)).toContain("walked");
});

it("the pinned adapter interrupts an in-flight model stream at its operation deadline", async () => {
  let aborted=false;
  const model = new MockLanguageModelV4({ doStream: async (options) => ({
    stream: new ReadableStream<LanguageModelV4StreamPart>({ start(controller) {
      expect(options.abortSignal).toBeDefined();
      controller.enqueue({ type: "stream-start", warnings: [] });
      options.abortSignal!.addEventListener("abort", () => controller.error(new DOMException("Model deadline elapsed", "AbortError")), { once: true });
    } }),
  }) });
  const agent = new WorkflowAgent({ model, stopWhen: isStepCount(1) });
  const result = await agent.stream({ prompt: "Wait indefinitely", timeout: 50, onAbort: async () => { aborted=true; } });
  expect(aborted).toBe(true);
  expect(model.doStreamCalls).toHaveLength(1);
  expect(result.steps).toHaveLength(0);
});

it("serializes actual adapter tool execution through its lifecycle hooks", async () => {
  let started!: () => void;
  let resume!: () => void;
  const firstStarted=new Promise<void>(resolve => { started=resolve; });
  const wait=new Promise<void>(resolve => { resume=resolve; });
  const events: string[]=[];
  const model = new MockLanguageModelV4({ doStream: async () => ({ stream: simulateReadableStream<LanguageModelV4StreamPart>({ chunks: [
    { type: "stream-start", warnings: [] },
    { type: "tool-call", toolCallId: "first", toolName: "read_note", input: '{"id":"first"}' },
    { type: "tool-call", toolCallId: "second", toolName: "read_note", input: '{"id":"second"}' },
    { type: "finish", finishReason: { unified: "tool-calls", raw: undefined }, usage },
  ], initialDelayInMs: 0, chunkDelayInMs: 0 }) }) });
  const order=serialToolLifecycle();
  const agent=new WorkflowAgent({ model, stopWhen: isStepCount(1),
    onToolExecutionStart: ({ toolCall }) => order.start(toolCall.toolCallId,async () => {}),
    onToolExecutionEnd: ({ toolCall }) => order.finish(toolCall.toolCallId,async () => {}),
    tools: { read_note: tool({ inputSchema: z.object({ id: z.string() }), execute: async ({ id }) => {
      events.push(`${id}:start`);
      if (id==="first") { started(); await wait; }
      events.push(`${id}:end`);
      return { text: id };
    } }) },
  });
  const running=agent.stream({ prompt: "Read two notes", timeout: 30_000 });
  await firstStarted;
  await Promise.resolve();
  expect(events).toEqual(["first:start"]);
  resume();
  await running;
  expect(events).toEqual(["first:start","first:end","second:start","second:end"]);
});
