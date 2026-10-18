import { createHmac, timingSafeEqual } from "node:crypto";

/** Must match liveTicketDomain in the Go API (voice_live_tokens.go). */
const domain = "misty-live-ticket\n";

export type LiveChannel = "mic" | "system";

/** Claims the Go API signs for one live transcription session. */
export type LiveTicket = {
  sid: string;
  sub: string;
  dev: string;
  ch: LiveChannel[];
  /** Total audio this session may stream, across channels, in ms. */
  max: number;
  lang?: string;
  model: string;
  /** Unix seconds; the WebSocket must connect before this. */
  exp: number;
};

function mac(secret: Buffer, encoded: string) {
  return createHmac("sha256", secret).update(domain + encoded).digest("hex");
}

/** Returns the claims of a valid, unexpired ticket, or null. */
export function verifyLiveTicket(ticket: string, secrets: Buffer[], now = Date.now()): LiveTicket | null {
  if (ticket.length > 4096) return null;
  const [version, encoded, signature, extra] = ticket.split(".");
  if (version !== "v1" || !encoded || !signature || extra !== undefined) return null;
  const provided = Buffer.from(signature, "hex");
  const valid = secrets.some((secret) => {
    if (!secret.length) return false;
    const expected = Buffer.from(mac(secret, encoded), "hex");
    return expected.length === provided.length && timingSafeEqual(expected, provided);
  });
  if (!valid) return null;
  let claims: LiveTicket;
  try {
    claims = JSON.parse(Buffer.from(encoded, "base64url").toString("utf8"));
  } catch {
    return null;
  }
  const channels = Array.isArray(claims.ch) ? claims.ch : [];
  if (
    typeof claims.sid !== "string" || !claims.sid ||
    typeof claims.sub !== "string" || !claims.sub ||
    typeof claims.model !== "string" || !claims.model ||
    !Number.isFinite(claims.max) || claims.max <= 0 ||
    !Number.isFinite(claims.exp) || now >= claims.exp * 1000 ||
    !channels.length || channels.some((channel) => channel !== "mic" && channel !== "system")
  )
    return null;
  return claims;
}

/**
 * Remembers spent session ids until their tickets expire, so a ticket opens
 * one stream. The Go API also settles each session once, so a second runtime
 * replica accepting the same ticket could not charge twice.
 */
export class SpentTickets {
  private spent = new Map<string, number>();

  spend(ticket: LiveTicket, now = Date.now()): boolean {
    for (const [sid, expires] of this.spent) if (expires <= now) this.spent.delete(sid);
    if (this.spent.has(ticket.sid)) return false;
    this.spent.set(ticket.sid, ticket.exp * 1000);
    return true;
  }
}
