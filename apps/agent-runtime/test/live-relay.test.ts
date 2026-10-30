import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import { liveSonioxKey } from "../src/live/credential.js";
import { LiveSession, type RelayMessage, type SttStream } from "../src/live/session.js";
import { signatureHeaders } from "../src/signature.js";
import { SpentTickets, verifyLiveTicket, type LiveTicket } from "../src/live/ticket.js";
import { speakerTurns } from "../src/transcribe-diarized.js";

const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./fixtures/${name}`, import.meta.url), "utf8"));
const secret = Buffer.alloc(32, "s");
/** Signed by the Go API's signLiveTicket; voice_live_tokens_test.go checks the same string. */
const goTicket = "v1.eyJzaWQiOiJzZXNzaW9uLTEiLCJzdWIiOiJ1c2VyLTEiLCJkZXYiOiJkZXZpY2UtMSIsImNoIjpbIm1pYyIsInN5c3RlbSJdLCJtYXgiOjkwMDAwMCwibW9kZWwiOiJzb25pb3gvc3R0LXJ0LXY0IiwiZXhwIjoxODAwMDAwMDYwfQ.117a28a26f8bf98cdd9d6a49c7037675f6e3c7f2b59d7eb7115f6162436d7417";
const before = 1_800_000_000_000;

describe("live tickets", () => {
  it("accepts the Go API's tickets until they expire", () => {
    const ticket = verifyLiveTicket(goTicket, [secret], before);
    expect(ticket).toMatchObject({ sid: "session-1", sub: "user-1", ch: ["mic", "system"], max: 900_000, model: "soniox/stt-rt-v4" });
    expect(verifyLiveTicket(goTicket, [secret], 1_800_000_060_000)).toBeNull();
    expect(verifyLiveTicket(goTicket, [Buffer.alloc(32, "x")], before)).toBeNull();
    expect(verifyLiveTicket(goTicket, [Buffer.alloc(32, "x"), secret], before)).not.toBeNull();
    expect(verifyLiveTicket(goTicket.replace("v1.", "v2."), [secret], before)).toBeNull();
    expect(verifyLiveTicket(`${goTicket}.extra`, [secret], before)).toBeNull();
  });

  it("opens one stream per ticket", () => {
    const spent = new SpentTickets();
    const ticket = verifyLiveTicket(goTicket, [secret], before)!;
    expect(spent.spend(ticket, before)).toBe(true);
    expect(spent.spend(ticket, before + 1000)).toBe(false);
  });
});

type Handlers = { result?: (r: unknown) => void; endpoint?: () => void; error?: (e: Error) => void };

/** Replays recorded Soniox results as audio arrives, like the provider. */
function replayStream(results: unknown[], configs: unknown[], channel = 1) {
  return (config: unknown): SttStream => {
    // Streams open in ticket channel order; only one channel hears the recording.
    const heard = configs.push(config) - 1 === channel ? results : [];
    const handlers: Handlers = {};
    let next = 0;
    const stream = {
      connect: vi.fn(async () => {}),
      sendAudio: vi.fn(() => {
        const result = heard[next++] as { tokens: { text: string }[] } | undefined;
        if (!result) return;
        handlers.result?.(result);
        if (result.tokens.some((token) => token.text === "<end>")) handlers.endpoint?.();
      }),
      finish: vi.fn(async () => {
        while (next < heard.length) stream.sendAudio();
      }),
      close: vi.fn(),
      on: (event: keyof Handlers, handler: never) => {
        handlers[event] = handler;
        return stream;
      },
    };
    return stream as unknown as SttStream;
  };
}

const ticket = (max = 60_000): LiveTicket => ({ sid: "s1", sub: "user-1", dev: "d1", ch: ["mic", "system"], max, model: "soniox/stt-rt-v4", exp: 0 });
const frame = (channel: number, ms: number) => Uint8Array.from([channel, ...new Uint8Array(ms * 32)]);

describe("live relay", () => {
  it("relays diarized segments per channel", async () => {
    const sent: RelayMessage[] = [];
    const configs: Record<string, unknown>[] = [];
    const session = new LiveSession(ticket(), (m) => sent.push(m), replayStream(fixture("soniox-live-two-speakers.json").results, configs) as never);
    await session.start({ type: "start", language: "en", context: { terms: ["Misty"], text: "Design review" } });
    expect(configs).toHaveLength(2);
    expect(configs[0]).toMatchObject({ model: "stt-rt-v4", enable_speaker_diarization: true, enable_endpoint_detection: true, sample_rate: 16000, context: { terms: ["Misty"], text: "Design review" } });
    expect(configs[0]).not.toHaveProperty("translation");
    expect(sent[0]).toEqual({ type: "ready", channels: ["mic", "system"], maxMs: 60_000 });
    for (let i = 0; i < 5; i++) session.audio(frame(1, 100));
    await session.finish();
    session.close();
    const finals = sent.flatMap((m) => (m.type === "segment" && m.segment.final ? [m.segment] : []));
    expect(finals.map((s) => [s.channel, s.speaker, s.sourceText])).toEqual([
      ["system", "1", "Can we ship Thursday?"],
      ["system", "2", "Yes, after review."],
    ]);
    expect(finals[0]?.words?.map((w) => w.text)).toEqual(["Can", "we", "ship", "Thursday?"]);
    expect(sent.at(-1)).toEqual({ type: "finished", usedMs: 500 });
  });

  it("stops at the ticket's limit and counts only what was sent", async () => {
    const sent: RelayMessage[] = [];
    const session = new LiveSession(ticket(250), (m) => sent.push(m), replayStream([], []) as never);
    await session.start({ type: "start" });
    session.audio(frame(0, 200));
    session.audio(frame(0, 200));
    session.audio(frame(0, 200));
    await vi.waitFor(() => expect(sent.at(-1)).toEqual({ type: "finished", usedMs: 250 }));
    expect(sent.some((m) => m.type === "limit")).toBe(true);
  });

  it("ignores audio for channels the ticket didn't grant", async () => {
    const micOnly = { ...ticket(), ch: ["mic"] as LiveTicket["ch"] };
    const session = new LiveSession(micOnly, () => {}, replayStream([], []) as never);
    await session.start({ type: "start" });
    session.audio(frame(1, 100));
    session.audio(frame(7, 100));
    session.close();
    expect(session.usedMs).toBe(0);
  });

  it("reports a provider failure without its details", async () => {
    const sent: RelayMessage[] = [];
    const failing = () => ({
      connect: async () => { throw new Error("401 api key sk-secret rejected"); },
      sendAudio() {}, finish: async () => {}, close() {}, on() { return this; },
    }) as unknown as SttStream;
    const session = new LiveSession(ticket(), (m) => sent.push(m), failing);
    await session.start({ type: "start" });
    expect(JSON.stringify(sent)).not.toContain("sk-secret");
    expect(sent.some((m) => m.type === "error")).toBe(true);
  });
});

describe("transcribe-diarized speaker turns", () => {
  it("groups a two-speaker transcript into turns and drops translations", () => {
    const turns = speakerTurns(fixture("soniox-async-two-speakers.json").tokens);
    expect(turns).toEqual([
      { speaker: "1", language: "en", start: 0, end: 0.9, text: "Morning everyone." },
      { speaker: "2", language: "en", start: 1.1, end: 1.7, text: "Hi Dana." },
      { speaker: "1", language: "en", start: 1.9, end: 2.5, text: "Let's start." },
    ]);
  });
});

describe("live session keys", () => {
  const route = (body: unknown, status = 200) =>
    vi.fn<typeof fetch>(async () => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }));

  it("asks Go for the session's route over the signed channel", async () => {
    vi.stubEnv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", secret.toString("base64"));
    const fetcher = route({ provider: "account", credential: { provider: "soniox", apiKey: "soniox-account-key" } });
    expect(await liveSonioxKey(ticket(), fetcher, { SONIOX_API_KEY: "instance-key" })).toBe("soniox-account-key");
    const [url, init] = fetcher.mock.calls[0] as [string, RequestInit];
    expect(url).toMatch(/\/internal\/voice\/live\/credential$/);
    expect(JSON.parse(String(init.body))).toEqual({ session_id: "s1", user: "user-1" });
    expect(new Headers(init.headers).get(signatureHeaders.signature)).toBeTruthy();
    vi.unstubAllEnvs();
  });

  it("uses the instance key for an instance route, and asks for a key with neither", async () => {
    vi.stubEnv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", secret.toString("base64"));
    expect(await liveSonioxKey(ticket(), route({ provider: "instance" }), { SONIOX_API_KEY: "instance-key" })).toBe("instance-key");
    await expect(liveSonioxKey(ticket(), route({ provider: "instance" }), {})).rejects.toThrow("model_key_needed");
    // An account key for another provider is never swapped for the instance's.
    await expect(liveSonioxKey(ticket(), route({ provider: "account", credential: { provider: "openai", apiKey: "k" } }), { SONIOX_API_KEY: "instance-key" })).rejects.toThrow("model_key_needed");
    vi.unstubAllEnvs();
  });

  it("refuses without repeating what Go sent", async () => {
    vi.stubEnv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", secret.toString("base64"));
    const leaked = "sk-echoed-secret";
    await expect(liveSonioxKey(ticket(), route({ error: leaked }, 500), {})).rejects.toThrow(/^live_route_unavailable: 500$/);
    const malformed = await liveSonioxKey(ticket(), route({ provider: "account", credential: { apiKey: leaked } }), {}).catch((e: Error) => e);
    expect((malformed as Error).message).toBe("invalid_model_route");
    vi.unstubAllEnvs();
  });
});
