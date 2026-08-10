import { describe, expect, it } from "vitest";

import { verifyControlRequestWithRotation } from "../src/control-protocol";

describe("control secret rotation", () => {
  it("accepts the current and retained previous secrets but no unrelated key", async () => {
    const current = crypto.getRandomValues(new Uint8Array(32));
    const previous = crypto.getRandomValues(new Uint8Array(32));
    const unrelated = crypto.getRandomValues(new Uint8Array(32));
    const body = new TextEncoder().encode('{"command":"acl","payload":{"acl_version":2}}');
    const timestamp = String(Math.floor(Date.now() / 1_000));

    await expect(
      verifyControlRequestWithRotation(
        toBase64(current),
        toBase64(previous),
        timestamp,
        body,
        await signature(previous, timestamp, body),
      ),
    ).resolves.toBe(true);
    await expect(
      verifyControlRequestWithRotation(
        toBase64(current),
        undefined,
        timestamp,
        body,
        await signature(previous, timestamp, body),
      ),
    ).resolves.toBe(false);
    await expect(
      verifyControlRequestWithRotation(
        toBase64(current),
        toBase64(previous),
        timestamp,
        body,
        await signature(unrelated, timestamp, body),
      ),
    ).resolves.toBe(false);
  });
});

async function signature(
  secret: Uint8Array<ArrayBufferLike>,
  timestamp: string,
  body: Uint8Array<ArrayBufferLike>,
): Promise<string> {
  const secretCopy = new Uint8Array<ArrayBuffer>(new ArrayBuffer(secret.byteLength));
  secretCopy.set(secret);
  const key = await crypto.subtle.importKey(
    "raw",
    secretCopy,
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const prefix = new TextEncoder().encode(`${timestamp}\n`);
  const message = new Uint8Array<ArrayBuffer>(
    new ArrayBuffer(prefix.byteLength + body.byteLength),
  );
  message.set(prefix);
  message.set(body, prefix.byteLength);
  const digest = await crypto.subtle.sign("HMAC", key, message);
  return toBase64Url(new Uint8Array(digest));
}

function toBase64(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes));
}

function toBase64Url(bytes: Uint8Array): string {
  return toBase64(bytes).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}
