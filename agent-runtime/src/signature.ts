import { createHash, createHmac, timingSafeEqual } from "node:crypto";

export const signatureHeaders = {
  timestamp: "x-misty-agent-timestamp",
  signature: "x-misty-agent-signature",
  idempotency: "idempotency-key",
} as const;

export function decodeControlSecret(
  value = process.env.MISTY_AGENT_RUNTIME_CONTROL_SECRET ?? "",
): Buffer {
  const secret = Buffer.from(value.trim(), "base64");
  if (secret.length < 32)
    throw new Error(
      "MISTY_AGENT_RUNTIME_CONTROL_SECRET must contain at least 32 base64-encoded bytes",
    );
  return secret;
}

export function signRequest(
  secret: Buffer,
  method: string,
  path: string,
  timestamp: string,
  body: Buffer,
): string {
  const digest = createHash("sha256").update(body).digest("hex");
  return createHmac("sha256", secret)
    .update(`${method.toUpperCase()}\n${path}\n${timestamp}\n${digest}`)
    .digest("hex");
}

export function verifyRequest(input: {
  secret: Buffer;
  previousSecret?: Buffer;
  method: string;
  path: string;
  timestamp: string;
  signature: string;
  body: Buffer;
  now?: number;
}): boolean {
  const seconds = Number(input.timestamp);
  const now = input.now ?? Date.now();
  if (!Number.isFinite(seconds) || Math.abs(now - seconds * 1000) > 5 * 60_000)
    return false;
  const provided = Buffer.from(input.signature, "hex");
  for (const secret of [input.secret, input.previousSecret]) {
    if (!secret?.length) continue;
    const expected = Buffer.from(
      signRequest(
        secret,
        input.method,
        input.path,
        input.timestamp,
        input.body,
      ),
      "hex",
    );
    if (
      provided.length === expected.length &&
      timingSafeEqual(provided, expected)
    )
      return true;
  }
  return false;
}
