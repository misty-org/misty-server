export type DocumentRole = "creator" | "editor" | "viewer";
export type CollaborationResourceType = "note" | "code" | "app" | "file";

export interface TicketClaims {
  iss: string;
  aud: string;
  jti: string;
  sub: string;
  space_id: string;
  resource_type: CollaborationResourceType;
  resource_id: string;
  note_id?: string;
  code_session_id?: string;
  room: string;
  role: DocumentRole;
  acl_version: number;
  exp: number;
}

interface TicketVerificationContext {
  publicKeyBase64: string;
  issuer: string;
  audience: string;
  room: string;
  resourceType: CollaborationResourceType;
  now?: number;
}

export class TicketError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = "TicketError";
  }
}

const MAX_TICKET_BYTES = 4096;
const VALID_ROLES: ReadonlySet<string> = new Set([
  "creator",
  "editor",
  "viewer",
]);
const VALID_RESOURCE_TYPES: ReadonlySet<string> = new Set(["note", "code", "app", "file"]);

function base64UrlToBytes(value: string): Uint8Array<ArrayBuffer> {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

function base64ToBytes(value: string): Uint8Array<ArrayBuffer> {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

async function importVerifyKey(publicKeyBase64: string): Promise<CryptoKey> {
  const raw = base64ToBytes(publicKeyBase64.trim());
  if (raw.byteLength !== 32) {
    throw new TicketError("ticket_key_misconfigured");
  }
  return crypto.subtle.importKey("raw", raw, { name: "Ed25519" }, false, [
    "verify",
  ]);
}

function decodeClaims(payloadSegment: string): TicketClaims {
  let parsed: unknown;
  try {
    parsed = JSON.parse(
      new TextDecoder().decode(base64UrlToBytes(payloadSegment)),
    );
  } catch {
    throw new TicketError("ticket_malformed");
  }
  const claims = parsed as Partial<TicketClaims>;
  if (
    typeof claims.iss !== "string" ||
    typeof claims.aud !== "string" ||
    typeof claims.jti !== "string" ||
    typeof claims.sub !== "string" ||
    typeof claims.space_id !== "string" ||
    typeof claims.resource_type !== "string" ||
    typeof claims.resource_id !== "string" ||
    typeof claims.room !== "string" ||
    typeof claims.role !== "string" ||
    typeof claims.acl_version !== "number" ||
    typeof claims.exp !== "number"
  ) {
    throw new TicketError("ticket_malformed");
  }
  if (!VALID_ROLES.has(claims.role)) {
    throw new TicketError("ticket_role_invalid");
  }
  if (
    !VALID_RESOURCE_TYPES.has(claims.resource_type) ||
    claims.resource_id.length < 1 ||
    claims.resource_id.length > 200
  ) {
    throw new TicketError("ticket_resource_invalid");
  }
  if (
    (claims.resource_type === "note" &&
      claims.note_id !== claims.resource_id) ||
    (claims.resource_type === "code" &&
      claims.code_session_id !== claims.resource_id) ||
    ((claims.resource_type === "app" || claims.resource_type === "file") &&
      !claims.resource_id.startsWith(`${claims.space_id}:`))
  ) {
    throw new TicketError("ticket_resource_invalid");
  }
  if (!Number.isInteger(claims.acl_version) || claims.acl_version < 1) {
    throw new TicketError("ticket_malformed");
  }
  if (claims.jti.length < 8 || claims.jti.length > 128) {
    throw new TicketError("ticket_malformed");
  }
  return claims as TicketClaims;
}

export async function verifyTicket(
  token: string,
  context: TicketVerificationContext,
): Promise<TicketClaims> {
  if (!token || token.length > MAX_TICKET_BYTES) {
    throw new TicketError("ticket_malformed");
  }
  const segments = token.split(".");
  if (segments.length !== 3) {
    throw new TicketError("ticket_malformed");
  }
  const [headerSegment, payloadSegment, signatureSegment] = segments as [
    string,
    string,
    string,
  ];

  let header: { alg?: unknown; typ?: unknown };
  try {
    header = JSON.parse(
      new TextDecoder().decode(base64UrlToBytes(headerSegment)),
    );
  } catch {
    throw new TicketError("ticket_malformed");
  }
  if (header.alg !== "EdDSA") {
    throw new TicketError("ticket_alg_unsupported");
  }

  const key = await importVerifyKey(context.publicKeyBase64);
  const signed = new TextEncoder().encode(`${headerSegment}.${payloadSegment}`);
  const signature = base64UrlToBytes(signatureSegment);
  const valid = await crypto.subtle.verify(
    { name: "Ed25519" },
    key,
    signature,
    signed,
  );
  if (!valid) {
    throw new TicketError("ticket_signature_invalid");
  }

  const claims = decodeClaims(payloadSegment);
  if (claims.iss !== context.issuer) {
    throw new TicketError("ticket_issuer_invalid");
  }
  if (claims.aud !== context.audience) {
    throw new TicketError("ticket_audience_invalid");
  }
  if (claims.room !== context.room) {
    throw new TicketError("ticket_room_mismatch");
  }
  if (claims.resource_type !== context.resourceType) {
    throw new TicketError("ticket_resource_mismatch");
  }
  const now = context.now ?? Math.floor(Date.now() / 1000);
  if (claims.exp <= now) {
    throw new TicketError("ticket_expired");
  }
  return claims;
}

export async function verifyTicketWithRotation(
  token: string,
  context: TicketVerificationContext,
  previousPublicKeyBase64?: string,
): Promise<TicketClaims> {
  try {
    return await verifyTicket(token, context);
  } catch (error) {
    if (
      !(error instanceof TicketError) ||
      error.code !== "ticket_signature_invalid" ||
      !previousPublicKeyBase64?.trim()
    ) {
      throw error;
    }
    return verifyTicket(token, {
      ...context,
      publicKeyBase64: previousPublicKeyBase64,
    });
  }
}
