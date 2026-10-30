import { RealtimeSttSession } from "@soniox/node";
import { defineWebSocketHandler } from "nitro";
import { ModelSetupError } from "../model-provider.js";
import { decodeControlSecret } from "../signature.js";
import { liveSonioxKey } from "./credential.js";
import { LiveSession, type SttFactory, type StartMessage } from "./session.js";
import { SpentTickets, verifyLiveTicket, type LiveTicket } from "./ticket.js";

const spent = new SpentTickets();
const sonioxURL = "wss://stt-rt.soniox.com/transcribe-websocket";

function liveTranscriptionEnabled(env: NodeJS.ProcessEnv = process.env) {
  return ["1", "true"].includes((env.MISTY_RUNTIME_LIVE_TRANSCRIBE ?? "").trim().toLowerCase());
}

const openSoniox = (apiKey: string): SttFactory => (config) =>
  new RealtimeSttSession(apiKey, sonioxURL, config, { connect_timeout_ms: 10_000 }) as never;

/** Why a session couldn't get a key, as a refusal the desktop can act on; never the key. */
function refusal(error: unknown): Response {
  const setup = error instanceof ModelSetupError;
  const status = setup ? (error.code === "model_key_needed" ? 409 : 400) : 503;
  const body = setup
    ? { code: error.code, message: error.detail }
    : { code: "live_transcription_unavailable", message: "Live transcription isn't available right now." };
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function secrets(): Buffer[] {
  const previous = process.env.MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS?.trim();
  return [decodeControlSecret(), ...(previous ? [Buffer.from(previous, "base64")] : [])];
}

type Context = { ticket: LiveTicket; apiKey: string; session?: LiveSession };

/**
 * /v1/live/transcribe?ticket=… — the desktop streams PCM16 per channel and
 * receives diarized segments. Needs a long-lived server (the Docker
 * node-server deployment); serverless functions cannot hold the socket.
 */
export default defineWebSocketHandler({
  async upgrade(request) {
    if (!liveTranscriptionEnabled()) throw new Response("Live transcription is not available.", { status: 404 });
    const ticket = verifyLiveTicket(new URL(request.url).searchParams.get("ticket") ?? "", secrets());
    if (!ticket || !spent.spend(ticket)) throw new Response("Unauthorized", { status: 401 });
    let apiKey: string;
    try {
      apiKey = await liveSonioxKey(ticket);
    } catch (error) {
      throw refusal(error);
    }
    return { context: { ticket, apiKey } satisfies Context };
  },
  open(peer) {
    const context = peer.context as Context;
    context.session = new LiveSession(
      context.ticket,
      (message) => peer.send(JSON.stringify(message)),
      openSoniox(context.apiKey),
    );
  },
  async message(peer, message) {
    const session = (peer.context as Context).session;
    if (!session) return;
    const bytes = message.uint8Array();
    // Audio frames start with a channel byte (0 or 1); control messages are JSON.
    if (bytes[0] !== 0x7b) return session.audio(bytes);
    let control: { type?: string };
    try {
      control = JSON.parse(message.text());
    } catch {
      return;
    }
    if (control.type === "start") await session.start(control as StartMessage);
    if (control.type === "finish") {
      await session.finish();
      peer.close(1000, "finished");
    }
  },
  close(peer) {
    (peer.context as Context).session?.close();
  },
});
