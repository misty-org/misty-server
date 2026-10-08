/**
 * Clipboard ticket verification.
 *
 * A ticket is an Ed25519-signed JWT minted by the Misty API for one admitted
 * device whose signed policy turns the clipboard on. It names the account's
 * room and lasts five minutes. The private key lives only on the Go server;
 * this Worker holds the public key, so a compromise here cannot mint tickets.
 */

export interface ClipboardTicketClaims {
  iss: string;
  aud: string;
  jti: string;
  sub: string;
  device_id: string;
  room: string;
  exp: number;
}

export interface TicketContext {
  publicKeyBase64: string;
  previousPublicKeyBase64?: string;
  issuer: string;
  audience: string;
  /** The room the request addresses. */
  room: string;
  /** Seconds since epoch; injectable for tests. */
  now?: number;
}

export class TicketError extends Error {
  constructor(readonly code: string) {
    // The message is the code alone, so logs never echo claim values.
    super(code);
    this.name = "TicketError";
  }
}

const MAX_TICKET_BYTES = 4096;
const ROOM_PATTERN = /^[0-9a-f]{64}$/;

function base64UrlToBytes(value: string): Uint8Array<ArrayBuffer> {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

function base64ToBytes(value: string): Uint8Array<ArrayBuffer> {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

export function isRoomId(value: string): boolean {
  return ROOM_PATTERN.test(value);
}

async function importVerifyKey(publicKeyBase64: string): Promise<CryptoKey> {
  const raw = base64ToBytes(publicKeyBase64.trim());
  if (raw.byteLength !== 32) throw new TicketError("ticket_key_misconfigured");
  return crypto.subtle.importKey("raw", raw, { name: "Ed25519" }, false, ["verify"]);
}

function decodeClaims(segment: string): ClipboardTicketClaims {
  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(base64UrlToBytes(segment)));
  } catch {
    throw new TicketError("ticket_malformed");
  }
  const claims = parsed as Partial<ClipboardTicketClaims>;
  if (
    typeof claims.iss !== "string" ||
    typeof claims.aud !== "string" ||
    typeof claims.jti !== "string" ||
    typeof claims.sub !== "string" ||
    typeof claims.device_id !== "string" ||
    typeof claims.room !== "string" ||
    typeof claims.exp !== "number" ||
    claims.device_id.length < 1 ||
    claims.device_id.length > 128 ||
    !isRoomId(claims.room)
  )
    throw new TicketError("ticket_malformed");
  return claims as ClipboardTicketClaims;
}

async function verifyWithKey(
  token: string,
  publicKeyBase64: string,
  context: TicketContext,
): Promise<ClipboardTicketClaims> {
  if (!token || token.length > MAX_TICKET_BYTES) throw new TicketError("ticket_malformed");
  const segments = token.split(".");
  if (segments.length !== 3) throw new TicketError("ticket_malformed");
  const [headerSegment, payloadSegment, signatureSegment] = segments as [string, string, string];
  let header: { alg?: unknown };
  try {
    header = JSON.parse(new TextDecoder().decode(base64UrlToBytes(headerSegment)));
  } catch {
    throw new TicketError("ticket_malformed");
  }
  // Pinning the algorithm defeats "none" and symmetric downgrades.
  if (header.alg !== "EdDSA") throw new TicketError("ticket_alg_unsupported");
  const key = await importVerifyKey(publicKeyBase64);
  const valid = await crypto.subtle.verify(
    { name: "Ed25519" },
    key,
    base64UrlToBytes(signatureSegment),
    new TextEncoder().encode(`${headerSegment}.${payloadSegment}`),
  );
  if (!valid) throw new TicketError("ticket_signature_invalid");
  const claims = decodeClaims(payloadSegment);
  if (claims.iss !== context.issuer) throw new TicketError("ticket_issuer_invalid");
  if (claims.aud !== context.audience) throw new TicketError("ticket_audience_invalid");
  // A ticket for one account's room never opens another's.
  if (claims.room !== context.room) throw new TicketError("ticket_room_mismatch");
  const now = context.now ?? Math.floor(Date.now() / 1000);
  if (claims.exp <= now) throw new TicketError("ticket_expired");
  return claims;
}

/**
 * Verifies with the active key, then the retained previous key only when the
 * signature does not match, so rotation never weakens claim checks.
 */
export async function verifyTicket(
  token: string,
  context: TicketContext,
): Promise<ClipboardTicketClaims> {
  try {
    return await verifyWithKey(token, context.publicKeyBase64, context);
  } catch (error) {
    if (
      !(error instanceof TicketError) ||
      error.code !== "ticket_signature_invalid" ||
      !context.previousPublicKeyBase64?.trim()
    )
      throw error;
    return verifyWithKey(token, context.previousPublicKeyBase64, context);
  }
}
