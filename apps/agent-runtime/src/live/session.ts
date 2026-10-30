import { SonioxCaptions, sonioxConfig, type SonioxContext, type SonioxResult } from "./soniox.js";
import type { Segment } from "./segment.js";
import type { LiveChannel, LiveTicket } from "./ticket.js";

/** The subset of @soniox/node's RealtimeSttSession the relay uses. */
export interface SttStream {
  connect(): Promise<void>;
  sendAudio(data: Uint8Array): void;
  finish(): Promise<void>;
  close(): void;
  on(event: "result", handler: (result: SonioxResult & { total_audio_proc_ms?: number }) => void): unknown;
  on(event: "endpoint" | "finished", handler: () => void): unknown;
  on(event: "error", handler: (error: Error) => void): unknown;
}
export type SttFactory = (config: ReturnType<typeof sonioxConfig>) => SttStream;

export type StartMessage = {
  type: "start";
  language?: string;
  context?: { text?: string; terms?: string[]; general?: { key: string; value: string }[] };
};

export type RelayMessage =
  | { type: "ready"; channels: LiveChannel[]; maxMs: number }
  | { type: "segment"; segment: Segment }
  | { type: "limit"; message: string }
  | { type: "error"; message: string }
  | { type: "finished"; usedMs: number };

const channelByte: Record<number, LiveChannel> = { 0: "mic", 1: "system" };
/** 16 kHz mono PCM16: 32 bytes per millisecond. */
const bytesPerMs = 32;

function boundedContext(context: StartMessage["context"]): SonioxContext | undefined {
  if (!context) return undefined;
  const terms = (context.terms ?? []).filter((t) => typeof t === "string" && t.trim()).map((t) => t.trim().slice(0, 160)).slice(0, 200);
  const general = (context.general ?? []).filter((g) => g && typeof g.key === "string" && typeof g.value === "string").slice(0, 10)
    .map((g) => ({ key: g.key.slice(0, 80), value: g.value.slice(0, 400) }));
  const text = typeof context.text === "string" ? context.text.slice(0, 4000) : undefined;
  return { ...(terms.length ? { terms } : {}), ...(general.length ? { general } : {}), ...(text ? { text } : {}) };
}

/**
 * One live session: a Soniox stream per channel, diarized within each, with
 * segments relayed to the desktop as they revise. Audio is counted as it
 * arrives and the session stops at its ticket's limit.
 */
export class LiveSession {
  private streams = new Map<LiveChannel, { stream: SttStream; captions: SonioxCaptions }>();
  private usedBytes = 0;
  private started = false;
  private ending?: Promise<void>;

  constructor(
    private readonly ticket: LiveTicket,
    private readonly send: (message: RelayMessage) => void,
    private readonly open: SttFactory,
  ) {}

  get usedMs() {
    return Math.floor(this.usedBytes / bytesPerMs);
  }

  async start(message: StartMessage) {
    if (this.started || this.ending) return;
    this.started = true;
    const language = typeof message.language === "string" && message.language.length <= 16 ? message.language : this.ticket.lang || "auto";
    const model = this.ticket.model.replace(/^soniox\//, "");
    const context = boundedContext(message.context);
    try {
      await Promise.all(this.ticket.ch.map(async (channel) => {
        const captions = new SonioxCaptions(`${this.ticket.sid}:${channel}`, language, language, (segment) => this.send({ type: "segment", segment }), { channel, noTranslation: true });
        const stream = this.open(sonioxConfig(language, language, { model, context, diarization: true }));
        stream.on("result", (result) => captions.ingest(result, result.total_audio_proc_ms ?? 0));
        stream.on("endpoint", () => captions.flush());
        // Provider errors can echo request details; report only that it failed.
        stream.on("error", () => this.fail("Live transcription stopped. Check your connection and try again."));
        this.streams.set(channel, { stream, captions });
        await stream.connect();
      }));
    } catch {
      this.fail("Live transcription couldn't start. Try again in a moment.");
      return;
    }
    this.send({ type: "ready", channels: this.ticket.ch, maxMs: this.ticket.max });
  }

  /** A binary frame: one channel byte (0 mic, 1 system), then PCM16 audio. */
  audio(frame: Uint8Array) {
    if (!this.started || this.ending || frame.length < 3) return;
    const channel = channelByte[frame[0] ?? -1];
    const target = channel && this.streams.get(channel);
    if (!target) return;
    const pcm = frame.subarray(1);
    const room = this.ticket.max * bytesPerMs - this.usedBytes;
    const allowed = pcm.subarray(0, Math.max(0, room - (room % 2)));
    if (allowed.length) {
      this.usedBytes += allowed.length;
      target.stream.sendAudio(allowed);
    }
    if (allowed.length < pcm.length) {
      this.send({ type: "limit", message: "This session reached its voice limit. What was heard so far is kept." });
      void this.finish();
    }
  }

  /** Graceful end: let Soniox finalize, then flush every lane. */
  finish(): Promise<void> {
    this.ending ??= (async () => {
      await Promise.all([...this.streams.values()].map(async ({ stream, captions }) => {
        try {
          await stream.finish();
        } catch {
          // A failed finish still flushes what was confirmed.
        }
        captions.flush();
      }));
      this.send({ type: "finished", usedMs: this.usedMs });
    })();
    return this.ending;
  }

  /** The desktop disconnected: stop streams without waiting. */
  close() {
    for (const { stream } of this.streams.values()) stream.close();
  }

  private fail(message: string) {
    if (this.ending) return;
    this.send({ type: "error", message });
    for (const { stream } of this.streams.values()) stream.close();
    this.ending = Promise.resolve();
  }
}
