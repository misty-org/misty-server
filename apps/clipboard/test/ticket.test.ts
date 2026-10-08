import { describe, expect, it } from "vitest";

import { TicketError, verifyTicket } from "../src/ticket";

const room = "a".repeat(64);

function base64Url(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

async function keyPair() {
  const pair = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, [
    "sign",
    "verify",
  ])) as CryptoKeyPair;
  const raw = new Uint8Array(
    (await crypto.subtle.exportKey("raw", pair.publicKey)) as ArrayBuffer,
  );
  return { privateKey: pair.privateKey, publicKeyBase64: btoa(String.fromCharCode(...raw)) };
}

async function mint(
  privateKey: CryptoKey,
  claims: Record<string, unknown>,
  alg = "EdDSA",
): Promise<string> {
  const encode = (value: unknown) => base64Url(new TextEncoder().encode(JSON.stringify(value)));
  const input = `${encode({ alg, typ: "JWT" })}.${encode(claims)}`;
  const signature = new Uint8Array(
    await crypto.subtle.sign({ name: "Ed25519" }, privateKey, new TextEncoder().encode(input)),
  );
  return `${input}.${base64Url(signature)}`;
}

const claims = {
  iss: "misty-api",
  aud: "misty-clipboard",
  jti: "clip_1234567890",
  sub: "user-1",
  device_id: "device-1",
  room,
  exp: 2_000,
};

const context = (publicKeyBase64: string) => ({
  publicKeyBase64,
  issuer: "misty-api",
  audience: "misty-clipboard",
  room,
  now: 1_000,
});

describe("clipboard tickets", () => {
  it("accepts a ticket for this room", async () => {
    const keys = await keyPair();
    const verified = await verifyTicket(await mint(keys.privateKey, claims), context(keys.publicKeyBase64));
    expect(verified.device_id).toBe("device-1");
  });

  it.each([
    ["ticket_room_mismatch", { room: "b".repeat(64) }],
    ["ticket_audience_invalid", { aud: "misty-journal-collab" }],
    ["ticket_expired", { exp: 1_000 }],
    ["ticket_malformed", { device_id: "" }],
  ])("rejects %s", async (code, override) => {
    const keys = await keyPair();
    const token = await mint(keys.privateKey, { ...claims, ...override });
    await expect(verifyTicket(token, context(keys.publicKeyBase64))).rejects.toThrow(
      new TicketError(code),
    );
  });

  it("rejects another key's signature and a downgraded algorithm", async () => {
    const keys = await keyPair();
    const other = await keyPair();
    await expect(
      verifyTicket(await mint(other.privateKey, claims), context(keys.publicKeyBase64)),
    ).rejects.toThrow(new TicketError("ticket_signature_invalid"));
    await expect(
      verifyTicket(await mint(keys.privateKey, claims, "none"), context(keys.publicKeyBase64)),
    ).rejects.toThrow(new TicketError("ticket_alg_unsupported"));
  });

  it("accepts the retained previous key during rotation", async () => {
    const current = await keyPair();
    const previous = await keyPair();
    const verified = await verifyTicket(await mint(previous.privateKey, claims), {
      ...context(current.publicKeyBase64),
      previousPublicKeyBase64: previous.publicKeyBase64,
    });
    expect(verified.sub).toBe("user-1");
  });
});
