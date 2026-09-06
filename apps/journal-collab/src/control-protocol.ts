const CONTROL_CLOCK_SKEW_SECONDS = 5 * 60;

export async function verifyControlRequest(
  encodedSecret: string,
  timestamp: string,
  body: Uint8Array,
  signature: string,
): Promise<boolean> {
  const issuedAt = Number(timestamp);
  if (!Number.isInteger(issuedAt)) return false;
  if (
    Math.abs(Math.floor(Date.now() / 1000) - issuedAt) >
    CONTROL_CLOCK_SKEW_SECONDS
  ) {
    return false;
  }

  let secret: Uint8Array;
  try {
    secret = Uint8Array.from(atob(encodedSecret.trim()), (character) =>
      character.charCodeAt(0),
    );
  } catch {
    return false;
  }
  if (secret.byteLength < 32 || !signature) return false;
  const key = await crypto.subtle.importKey(
    "raw",
    secret,
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const prefix = new TextEncoder().encode(`${timestamp}\n`);
  const signed = new Uint8Array(prefix.byteLength + body.byteLength);
  signed.set(prefix);
  signed.set(body, prefix.byteLength);
  const digest = new Uint8Array(await crypto.subtle.sign("HMAC", key, signed));
  return constantTimeEqual(base64URL(digest), signature);
}

export async function verifyControlRequestWithRotation(
  currentSecret: string,
  previousSecret: string | undefined,
  timestamp: string,
  body: Uint8Array,
  signature: string,
): Promise<boolean> {
  if (await verifyControlRequest(currentSecret, timestamp, body, signature)) {
    return true;
  }
  return Boolean(
    previousSecret?.trim() &&
      (await verifyControlRequest(previousSecret, timestamp, body, signature)),
  );
}

export async function signServiceRequest(
  encodedSecret: string,
  timestamp: string,
  body: Uint8Array,
): Promise<string> {
  const secret = Uint8Array.from(atob(encodedSecret.trim()), (character) =>
    character.charCodeAt(0),
  );
  const key = await crypto.subtle.importKey(
    "raw", secret, { name: "HMAC", hash: "SHA-256" }, false, ["sign"],
  );
  const prefix = new TextEncoder().encode(`${timestamp}\n`);
  const signed = new Uint8Array(prefix.byteLength + body.byteLength);
  signed.set(prefix); signed.set(body, prefix.byteLength);
  return base64URL(new Uint8Array(await crypto.subtle.sign("HMAC", key, signed)));
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function jsonResponse(
  body: unknown,
  status = 200,
  headers?: HeadersInit,
): Response {
  const responseHeaders = new Headers(headers);
  responseHeaders.set("Content-Type", "application/json");
  return new Response(JSON.stringify(body), {
    status,
    headers: responseHeaders,
  });
}

function base64URL(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/u, "");
}

function constantTimeEqual(left: string, right: string): boolean {
  const length = Math.max(left.length, right.length);
  let mismatch = left.length ^ right.length;
  for (let index = 0; index < length; index += 1) {
    mismatch |= (left.charCodeAt(index) || 0) ^ (right.charCodeAt(index) || 0);
  }
  return mismatch === 0;
}
