import { RealtimeSttSession } from "@soniox/node";
import { defineWebSocketHandler } from "nitro";
import { decodeControlSecret } from "../signature.js";
import { LiveSession, type SttFactory, type StartMessage } from "./session.js";
import { settleLiveSession } from "./settle.js";
import { SpentTickets, verifyLiveTicket, type LiveTicket } from "./ticket.js";

const spent = new SpentTickets();
const sonioxURL = "wss://stt-rt.soniox.com/transcribe-websocket";

export function liveTranscriptionEnabled(env: NodeJS.ProcessEnv = process.env) {
  return ["1", "true"].includes((env.MISTY_RUNTIME_LIVE_TRANSCRIBE ?? "").trim().toLowerCase()) && !!env.SONIOX_API_KEY?.trim();
}

const openSoniox: SttFactory = (config) =>
  new RealtimeSttSession(process.env.SONIOX_API_KEY!.trim(), sonioxURL, config, { connect_timeout_ms: 10_000 }) as never;

function secrets(): Buffer[] {
  const previous = process.env.MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS?.trim();
  return [decodeControlSecret(), ...(previous ? [Buffer.from(previous, "base64")] : [])];
}

type Context = { ticket: LiveTicket; session?: LiveSession };

/**
 * /v1/live/transcribe?ticket=… — the desktop streams PCM16 per channel and
 * receives diarized segments. Needs a long-lived server (the Docker
 * node-server deployment); serverless functions cannot hold the socket.
 */
export default defineWebSocketHandler({
  upgrade(request) {
    if (!liveTranscriptionEnabled()) throw new Response("Live transcription is not available.", { status: 404 });
    const ticket = verifyLiveTicket(new URL(request.url).searchParams.get("ticket") ?? "", secrets());
    if (!ticket || !spent.spend(ticket)) throw new Response("Unauthorized", { status: 401 });
    return { context: { ticket } satisfies Context };
  },
  open(peer) {
    const context = peer.context as Context;
    context.session = new LiveSession(
      context.ticket,
      (message) => peer.send(JSON.stringify(message)),
      openSoniox,
      (usedMs) => settleLiveSession({ session_id: context.ticket.sid, user: context.ticket.sub, used_ms: usedMs }),
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
