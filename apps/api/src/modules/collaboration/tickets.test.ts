import { createPublicKey, generateKeyPairSync } from "node:crypto";
import { expect, it } from "vitest";
import { loadCollaborationConfig } from "./config.js";
import { createCollaborationTickets } from "./tickets.js";
import { verifyTicket } from "../../../../journal-collab/src/ticket.js";
import { socketIsReadOnly } from "../../../../journal-collab/src/room-policy.js";

const key = generateKeyPairSync("ed25519").privateKey;
const env = { JOURNAL_COLLAB_TICKET_PRIVATE_KEY: key.export({ type: "pkcs8", format: "der" }).toString("base64"),
  JOURNAL_COLLAB_ROOM_SALT: Buffer.alloc(32, 11).toString("base64"), JOURNAL_COLLAB_CONTROL_SECRET: Buffer.alloc(32, 12).toString("base64"),
  JOURNAL_COLLAB_PROJECTION_SECRET: Buffer.alloc(32, 13).toString("base64") };
const now = new Date("2026-09-05T12:00:00Z");

it("issues note/drawing proofs accepted by the managed worker's real verifier and role policy", async () => {
  const config = loadCollaborationConfig(env)!, sign = createCollaborationTickets(config, () => now);
  const publicKeyBase64 = Buffer.from(createPublicKey(key).export({ format: "jwk" }).x!, "base64url").toString("base64");
  const rooms = new Set();
  for (const kind of ["note", "drawing"] as const) for (const role of ["creator", "editor", "viewer"] as const) {
    const proof = await sign({ userId: "test-user", spaceId: "test-space", kind, id: "same-resource-id", role, aclVersion: 3 });
    const context = { publicKeyBase64, issuer: config.issuer, audience: config.audience, room: proof.room, resourceType: kind, now: now.getTime() / 1000 };
    const claims = await verifyTicket(proof.ticket, context);
    expect(claims).toMatchObject({ sub: "test-user", space_id: "test-space", role, acl_version: 3, resource_id: "same-resource-id", exp: now.getTime() / 1000 + 60 });
    expect(proof.url).toBe(`wss://misty-journal-collab.mistysys.workers.dev/parties/${kind}-room/${proof.room}`);
    expect(socketIsReadOnly({ userID: claims.sub, role, aclVersion: 3, resourceID: claims.resource_id, spaceID: claims.space_id }, 3)).toBe(role === "viewer");
    await expect(verifyTicket(proof.ticket, { ...context, room: "wrong-room" })).rejects.toThrow("ticket_room_mismatch");
    await expect(verifyTicket(proof.ticket, { ...context, now: claims.exp })).rejects.toThrow("ticket_expired");
    rooms.add(proof.room);
  }
  expect(rooms.size).toBe(2);
  const exported = await sign({ userId: "test-user", spaceId: "test-space", kind: "note", id: "export", role: "creator", aclVersion: 1 }, true);
  expect(exported.role).toBe("viewer"); expect(exported.expires_at).toBe("2026-09-05T12:15:00.000Z");
});

it("preserves local self-host origins and rejects incomplete, unsafe or malformed configuration", () => {
  expect(loadCollaborationConfig({})).toBeNull();
  expect(loadCollaborationConfig({ ...env, MISTY_DEPLOYMENT_MODE: "self_hosted", MISTY_COLLAB_PUBLIC_URL: "http://127.0.0.1:8084/" })?.origin).toBe("http://127.0.0.1:8084");
  for (const extra of [{ JOURNAL_COLLAB_CONTROL_SECRET: "" }, { JOURNAL_COLLAB_ROOM_SALT: "not-base64" }, { PARTYKIT_HOST: "host.invalid/path" },
    { MISTY_DEPLOYMENT_MODE: "self_hosted", MISTY_COLLAB_PUBLIC_URL: "http://external.invalid" },
    { MISTY_DEPLOYMENT_MODE: "self_hosted", MISTY_COLLAB_PUBLIC_URL: "https://user:secret@host.invalid" },
    { MISTY_DEPLOYMENT_MODE: "self_hosted", MISTY_COLLAB_PUBLIC_URL: "https://host.invalid/?token=secret" }]) {
    expect(() => loadCollaborationConfig({ ...env, ...extra })).toThrow("Invalid Journal collaboration configuration");
  }
});
