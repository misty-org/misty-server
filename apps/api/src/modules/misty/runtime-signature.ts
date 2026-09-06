import { createHash, createHmac, timingSafeEqual } from "node:crypto";

/** Matches apps/agent-runtime/src/signature.ts and the Go runtime protocol. */
export function verifyRuntimeSignature(input: {
  method: string; path: string; timestamp: string; signature: string; body: Uint8Array;
  secrets: readonly Uint8Array[]; now: number;
}): boolean {
  if (!/^\d{1,12}$/.test(input.timestamp) || !/^[a-fA-F0-9]{64}$/.test(input.signature)
    || Math.abs(input.now - Number(input.timestamp) * 1000) > 300_000) return false;
  const digest = createHash("sha256").update(input.body).digest("hex");
  const message = [input.method.toUpperCase(), input.path, input.timestamp, digest].join("\n");
  const provided = Buffer.from(input.signature, "hex");
  return input.secrets.some(secret => secret.length >= 32 && timingSafeEqual(provided, createHmac("sha256", secret).update(message).digest()));
}
