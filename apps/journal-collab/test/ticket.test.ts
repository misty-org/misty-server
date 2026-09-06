import { describe, expect, it } from "vitest";

import {
  TicketError,
  verifyTicket,
  verifyTicketWithRotation,
  type TicketClaims,
} from "../src/ticket";

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function base64Url(bytes: Uint8Array): string {
  return bytesToBase64(bytes)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function encodeSegment(value: unknown): string {
  return base64Url(new TextEncoder().encode(JSON.stringify(value)));
}

/** Mints tickets the way the Go server will, so tests exercise the real format. */
async function issuer() {
  const pair = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, [
    "sign",
    "verify",
  ])) as CryptoKeyPair;
  const rawPublic = new Uint8Array(
    (await crypto.subtle.exportKey("raw", pair.publicKey)) as ArrayBuffer,
  );
  return {
    publicKeyBase64: bytesToBase64(rawPublic),
    async mint(
      claims: Partial<TicketClaims> = {},
      header: Record<string, unknown> = {},
    ): Promise<string> {
      const fullClaims: TicketClaims = {
        iss: "misty-api",
        aud: "misty-journal-collab",
        jti: "ticket-0000000001",
        sub: "user_1",
        space_id: "space_1",
        resource_type: "note",
        resource_id: "note_1",
        note_id: "note_1",
        room: "room_1",
        role: "editor",
        acl_version: 3,
        exp: Math.floor(Date.now() / 1000) + 60,
        ...claims,
      };
      const headerSegment = encodeSegment({
        alg: "EdDSA",
        typ: "JWT",
        ...header,
      });
      const payloadSegment = encodeSegment(fullClaims);
      const signature = new Uint8Array(
        await crypto.subtle.sign(
          { name: "Ed25519" },
          pair.privateKey,
          new TextEncoder().encode(`${headerSegment}.${payloadSegment}`),
        ),
      );
      return `${headerSegment}.${payloadSegment}.${base64Url(signature)}`;
    },
  };
}

function verificationContext(publicKeyBase64: string) {
  return {
    publicKeyBase64,
    issuer: "misty-api",
    audience: "misty-journal-collab",
    room: "room_1",
    resourceType: "note" as const,
  };
}

async function expectRejection(promise: Promise<unknown>, code: string) {
  await expect(promise).rejects.toThrow(TicketError);
  await expect(promise).rejects.toThrow(code);
}

describe("collaboration ticket verification", () => {
  it("accepts the retained previous public key only during rotation", async () => {
    const current = await issuer();
    const previous = await issuer();
    const token = await previous.mint();

    await expect(
      verifyTicketWithRotation(
        token,
        verificationContext(current.publicKeyBase64),
        previous.publicKeyBase64,
      ),
    ).resolves.toMatchObject({ sub: "user_1" });
    await expectRejection(
      verifyTicketWithRotation(
        token,
        verificationContext(current.publicKeyBase64),
      ),
      "ticket_signature_invalid",
    );
  });

  it("accepts a well-formed ticket for the right room", async () => {
    const api = await issuer();
    const token = await api.mint();

    const claims = await verifyTicket(
      token,
      verificationContext(api.publicKeyBase64),
    );

    expect(claims.sub).toBe("user_1");
    expect(claims.role).toBe("editor");
    expect(claims.acl_version).toBe(3);
  });

  it("rejects a ticket signed by a different key", async () => {
    const api = await issuer();
    const impostor = await issuer();
    const token = await impostor.mint();

    await expectRejection(
      verifyTicket(token, verificationContext(api.publicKeyBase64)),
      "ticket_signature_invalid",
    );
  });

  it("rejects a tampered payload", async () => {
    const api = await issuer();
    const token = await api.mint({ role: "viewer" });
    const [header, , signature] = token.split(".") as [string, string, string];
    // Swap the claims for an escalated set, keeping the original signature.
    const escalated = encodeSegment({
      iss: "misty-api",
      aud: "misty-journal-collab",
      jti: "ticket-0000000001",
      sub: "user_1",
      space_id: "space_1",
      resource_type: "note",
      resource_id: "note_1",
      note_id: "note_1",
      room: "room_1",
      role: "creator",
      acl_version: 3,
      exp: Math.floor(Date.now() / 1000) + 60,
    });

    await expectRejection(
      verifyTicket(
        `${header}.${escalated}.${signature}`,
        verificationContext(api.publicKeyBase64),
      ),
      "ticket_signature_invalid",
    );
  });

  it("refuses an unsigned or downgraded algorithm", async () => {
    const api = await issuer();
    for (const alg of ["none", "HS256", "RS256"]) {
      const token = await api.mint({}, { alg });
      await expectRejection(
        verifyTicket(token, verificationContext(api.publicKeyBase64)),
        "ticket_alg_unsupported",
      );
    }
  });

  it("rejects a ticket minted for a different room", async () => {
    const api = await issuer();
    const token = await api.mint({
      room: "room_other",
      resource_id: "note_other",
      note_id: "note_other",
    });

    await expectRejection(
      verifyTicket(token, verificationContext(api.publicKeyBase64)),
      "ticket_room_mismatch",
    );
  });

  it("rejects wrong issuer and wrong audience", async () => {
    const api = await issuer();
    const context = verificationContext(api.publicKeyBase64);

    await expectRejection(
      verifyTicket(await api.mint({ iss: "someone-else" }), context),
      "ticket_issuer_invalid",
    );
    await expectRejection(
      verifyTicket(await api.mint({ aud: "another-service" }), context),
      "ticket_audience_invalid",
    );
  });

  it("rejects an expired ticket", async () => {
    const api = await issuer();
    const now = Math.floor(Date.now() / 1000);
    const token = await api.mint({ exp: now + 60 });
    const context = verificationContext(api.publicKeyBase64);

    // Valid a second before expiry, rejected once the clock passes it.
    await expect(
      verifyTicket(token, { ...context, now: now + 59 }),
    ).resolves.toBeTruthy();
    await expectRejection(
      verifyTicket(token, { ...context, now: now + 61 }),
      "ticket_expired",
    );
  });

  it("rejects malformed tokens and invalid roles", async () => {
    const api = await issuer();
    const context = verificationContext(api.publicKeyBase64);

    await expectRejection(verifyTicket("", context), "ticket_malformed");
    await expectRejection(verifyTicket("a.b", context), "ticket_malformed");
    await expectRejection(
      verifyTicket("not-a-token-at-all", context),
      "ticket_malformed",
    );
    await expectRejection(
      verifyTicket(await api.mint({ role: "owner" as never }), context),
      "ticket_role_invalid",
    );
    await expectRejection(
      verifyTicket(await api.mint({ acl_version: 0 }), context),
      "ticket_malformed",
    );
  });

  it("fails loudly on a misconfigured public key rather than blaming the client", async () => {
    const api = await issuer();

    await expectRejection(
      verifyTicket(await api.mint(), verificationContext("dG9vLXNob3J0")),
      "ticket_key_misconfigured",
    );
  });

  it("never leaks claim values in the error", async () => {
    const api = await issuer();
    const impostor = await issuer();
    const token = await impostor.mint({
      resource_id: "note_secret_title",
      note_id: "note_secret_title",
      sub: "user_private",
    });

    const error = await verifyTicket(
      token,
      verificationContext(api.publicKeyBase64),
    ).catch((caught: Error) => caught);

    expect(String(error)).not.toContain("note_secret_title");
    expect(String(error)).not.toContain("user_private");
  });

  it("rejects a drawing ticket presented to a note room", async () => {
    const api = await issuer();
    const token = await api.mint({
      resource_type: "drawing",
      resource_id: "drawing_1",
      note_id: undefined,
      drawing_id: "drawing_1",
    });

    await expectRejection(
      verifyTicket(token, verificationContext(api.publicKeyBase64)),
      "ticket_resource_mismatch",
    );
  });

  it("accepts a drawing ticket in the matching drawing room", async () => {
    const api = await issuer();
    const token = await api.mint({
      resource_type: "drawing",
      resource_id: "drawing_1",
      note_id: undefined,
      drawing_id: "drawing_1",
    });

    const claims = await verifyTicket(token, {
      ...verificationContext(api.publicKeyBase64),
      resourceType: "drawing",
    });

    expect(claims.drawing_id).toBe("drawing_1");
    expect(claims.resource_type).toBe("drawing");
  });
});
