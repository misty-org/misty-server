import { describe, expect, it } from "vitest";
import { watchStream } from "../src/instance-model.js";

const streamOf = (parts: string[], hangAfter = false) =>
  new ReadableStream<string>({
    start(controller) {
      for (const part of parts) controller.enqueue(part);
      if (!hangAfter) controller.close();
    },
  });

async function drain(stream: ReadableStream<string>) {
  const parts: string[] = [];
  for await (const part of stream) parts.push(part);
  return parts;
}

describe("watchStream", () => {
  it("passes a healthy stream through", async () => {
    const result = await watchStream(async () => ({ stream: streamOf(["a", "b"]) }), undefined, 50);
    expect(await drain(result.stream)).toEqual(["a", "b"]);
  });

  it("fails a stream that stops sending", async () => {
    let aborted = false;
    const result = await watchStream(
      async (signal) => {
        signal.addEventListener("abort", () => (aborted = true));
        return { stream: streamOf(["a"], true) };
      },
      undefined,
      30,
    );
    await expect(drain(result.stream)).rejects.toThrow("model_stream_stalled");
    expect(aborted).toBe(true);
  });

  it("fails a response that never starts", async () => {
    await expect(
      watchStream(
        (signal) =>
          new Promise<{ stream: ReadableStream<string> }>((_, reject) =>
            signal.addEventListener("abort", () => reject(new Error("aborted"))),
          ),
        undefined,
        30,
      ),
    ).rejects.toThrow("model_stream_stalled");
  });
});
