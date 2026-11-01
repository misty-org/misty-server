import { defineWebSocketHandler } from "nitro";
import { desktopRuntime } from "../device-models.js";
import { decodeControlSecret } from "../signature.js";
import { SpentTickets, verifyLiveTicket } from "../live/ticket.js";
import { deviceVoiceModels } from "./models.js";
import { VoiceSession } from "./session.js";

const spent = new SpentTickets();
/** The ticket's model names what it opens; a live transcription ticket can't open a voice session. */
const voiceTicketModel = "misty/voice-conversation";
const sessionMs = 5 * 60_000;

type Context = { sid: string; session?: VoiceSession; timer?: ReturnType<typeof setTimeout> };

/**
 * /v1/device/voice?ticket=… — the companion's spoken conversation on this computer's runtime
 * (src/voice/session.ts). The app mints the one-time ticket with this launch's secret.
 */
export default defineWebSocketHandler({
  upgrade(request) {
    if (!desktopRuntime()) throw new Response("Not found", { status: 404 });
    const ticket = verifyLiveTicket(new URL(request.url).searchParams.get("ticket") ?? "", [decodeControlSecret()]);
    if (!ticket || ticket.model !== voiceTicketModel || !spent.spend(ticket)) throw new Response("Unauthorized", { status: 401 });
    return { context: { sid: ticket.sid.slice(0, 16) } satisfies Context };
  },
  open(peer) {
    const context = peer.context as Context;
    context.session = new VoiceSession(context.sid, (event) => peer.send(JSON.stringify(event)), deviceVoiceModels);
    context.timer = setTimeout(() => {
      peer.send(JSON.stringify({ type: "session.expired" }));
      peer.close(1000, "expired");
    }, sessionMs);
  },
  async message(peer, message) {
    const session = (peer.context as Context).session;
    if (!session || message.text().length > 200_000) return;
    let event: Record<string, unknown>;
    try {
      event = JSON.parse(message.text());
    } catch {
      return;
    }
    await session.handle(event);
  },
  close(peer) {
    const context = peer.context as Context;
    clearTimeout(context.timer);
    context.session?.close();
  },
});
