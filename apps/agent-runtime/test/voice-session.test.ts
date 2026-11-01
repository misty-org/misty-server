import { describe, expect, it, vi } from "vitest";
import { VoiceSession, type VoiceModels } from "../src/voice/session.js";
import { splitSentences } from "../src/voice/prompt.js";
import { wav } from "../src/voice/models.js";

function harness(replies: Array<{ say?: string; tool?: string; instruction?: string }>, heard = "what time is it") {
  const sent: Array<Record<string, unknown>> = [];
  const tools: string[][] = [];
  const models: VoiceModels = {
    transcribe: vi.fn(async () => heard),
    reply: vi.fn(async (_system, _messages, offered) => {
      tools.push(offered);
      const next = replies.shift() ?? {};
      return { say: next.say ?? "", tool: next.tool ?? "", instruction: next.instruction ?? "" };
    }),
    speak: vi.fn(async (text: string) => Buffer.from(`pcm:${text}`)),
  };
  const session = new VoiceSession("abc", (event) => sent.push(event), models);
  const types = () => sent.map((event) => event.type);
  return { session, sent, types, models, tools };
}

describe("the voice session on the device", () => {
  it("hears a turn, answers it aloud and saves it once playback ends", async () => {
    const { session, sent, types } = harness([{ say: "It's three o'clock. Anything else today?" }]);
    await session.handle({ type: "turn.begin" });
    await session.handle({ type: "audio.append", sequence: 0, audio: Buffer.from("pcm").toString("base64") });
    await session.handle({ type: "audio.commit" });
    expect(types()).toEqual(["ready", "turn.ready", "transcript", "audio", "audio", "audio.done"]);
    expect(sent[2]).toMatchObject({ id: "voice-abc-1", text: "what time is it" });
    await session.handle({ type: "playback.done" });
    expect(sent.at(-1)).toEqual({ type: "turn.done", id: "voice-abc-1", prompt: "what time is it", reply: "It's three o'clock. Anything else today?", interrupted: false });
  });

  it("hands work to the app and says nothing once it's admitted", async () => {
    const { session, sent, types } = harness([{ tool: "start_task", instruction: "make a playlist of the videos" }, { tool: "steer_task", instruction: "add more" }]);
    await session.handle({ type: "context", history: "[]", task: "" });
    await session.handle({ type: "turn.begin" });
    await session.handle({ type: "text.commit", text: "make a playlist of the videos" });
    const call = sent.find((event) => event.type === "tool.call")!;
    expect(call).toMatchObject({ name: "start_task", instruction: "make a playlist of the videos", key: "voice-abc-1:start_task" });
    await session.handle({ type: "tool.result", callId: call.callId, invocationId: "invocation_1" });
    expect(types().at(-1)).toBe("audio.done");
    await session.handle({ type: "playback.done" });
    expect(sent.at(-1)).toMatchObject({ type: "turn.done", reply: "" });
    // The next steer names the task the app admitted.
    await session.handle({ type: "turn.begin" });
    await session.handle({ type: "text.commit", text: "add more" });
    expect(sent.at(-1)).toMatchObject({ type: "tool.call", name: "steer_task", invocationId: "invocation_1" });
  });

  it("answers get_context from the chat the app sent, without asking the app", async () => {
    const { session, sent, tools, models } = harness([{ tool: "get_context" }, { say: "You asked about Lisbon earlier." }]);
    await session.handle({ type: "context", history: '[{"user":"trip to Lisbon"}]', task: "invocation_9" });
    await session.handle({ type: "turn.begin" });
    await session.handle({ type: "text.commit", text: "what did we talk about" });
    expect(sent.some((event) => event.type === "tool.call")).toBe(false);
    expect(tools[0]).toContain("start_task");
    const second = vi.mocked(models.reply).mock.calls[1]?.[1] ?? [];
    expect(JSON.stringify(second)).toContain("trip to Lisbon");
  });

  it("reads a finished task aloud with no tools at all", async () => {
    const { session, tools, sent } = harness([{ say: "Your playlist is ready." }]);
    await session.handle({ type: "turn.begin" });
    await session.handle({ type: "task.read", invocationId: "invocation_1", result: "Made a playlist of 5 videos." });
    expect(tools[0]).toEqual([]);
    expect(sent.some((event) => event.type === "audio")).toBe(true);
  });

  it("stops speaking when the person interrupts and saves the turn as interrupted", async () => {
    const { session, sent } = harness([{ say: "A long answer." }]);
    await session.handle({ type: "turn.begin" });
    await session.handle({ type: "text.commit", text: "tell me everything" });
    await session.handle({ type: "turn.cancel" });
    expect(sent.at(-1)).toMatchObject({ type: "turn.done", interrupted: true });
  });

  it("refuses audio out of order and ends after eight turns", async () => {
    const out = harness([]);
    await out.session.handle({ type: "turn.begin" });
    await out.session.handle({ type: "audio.append", sequence: 3, audio: "AAAA" });
    expect(out.sent.at(-1)?.type).toBe("error");
    const { session, types } = harness(Array.from({ length: 9 }, () => ({ say: "" })));
    for (let i = 0; i < 9; i++) {
      await session.handle({ type: "turn.begin" });
      if (i < 8) {
        await session.handle({ type: "text.commit", text: "hi" });
        await session.handle({ type: "playback.done" });
      }
    }
    expect(types().at(-1)).toBe("session.expired");
  });

  it("cuts replies into sentences worth speaking alone", () => {
    expect(splitSentences("Hi. It's three o'clock now. Anything else?")).toEqual(["Hi. It's three o'clock now.", "Anything else?"]);
    expect(wav(Buffer.alloc(10)).subarray(0, 4).toString()).toBe("RIFF");
  });
});
