import { createHmac, generateKeyPairSync } from "node:crypto";
import { expect, it } from "vitest";
import { createControlSender } from "./control.js";
import { collaborationRoom } from "./tickets.js";

const config = { origin: "https://collaboration.example.invalid", privateKey: generateKeyPairSync("ed25519").privateKey,
  roomSalt: Buffer.alloc(32, 10), controlSecret: Buffer.alloc(32, 11), projectionSecret: Buffer.alloc(32, 12), previousProjectionSecret: null,
  issuer: "misty-api", audience: "misty-journal-collab" };

it("signs exact control bytes and discloses resource IDs only to the self-host service", async () => {
  for (const deployment of ["hosted", "self_hosted"] as const) {
    const send = createControlSender(config, deployment, async (url, init) => {
      expect(url).toBe(`${config.origin}/parties/note-room/${collaborationRoom(config, "note", "private-note")}`);
      expect(init?.redirect).toBe("error"); expect(init?.signal).toBeInstanceOf(AbortSignal);
      const headers = new Headers(init?.headers);
      expect(headers.get("X-Misty-Resource-ID")).toBe(deployment === "hosted" ? null : "private-note");
      expect(headers.get("X-Misty-Signature")).toBe(createHmac("sha256", config.controlSecret).update(`${headers.get("X-Misty-Timestamp")}\n${init?.body}`).digest("base64url"));
      expect(JSON.parse(String(init?.body))).toEqual({ command: "acl", payload: { acl_version: 3 } });
      return Response.json({ ok: true, acl_version: 4 });
    });
    await send("note", "private-note", "acl", { acl_version: 3 });
  }
});

it("requires semantic acknowledgements and bounds upstream responses", async () => {
  for (const [command, payload, response] of [
    ["purge", {}, Response.json({ ok: true })],
    ["acl", { acl_version: 3 }, Response.json({ ok: true, acl_version: 2 })],
    ["disconnect", {}, Response.json({ ok: true }, { status: 500 })],
    ["purge", {}, new Response("x".repeat(65537))],
    ["purge", {}, Response.json({ code: "other" }, { status: 410 })],
  ] as const) await expect(createControlSender(config, "hosted", async () => response)("drawing", "drawing-id", command, payload)).rejects.toThrow();
  await expect(createControlSender(config, "hosted", async () => Response.json({ code: "resource_deleted" }, { status: 410 }))("note", "note-id", "purge", {})).resolves.toBeUndefined();
  let calls = 0;
  const invalid = createControlSender(config, "hosted", async () => { calls++; return Response.json({ ok: true }); });
  for (const payload of [{}, { acl_version: 0 }, { acl_version: "2" }, { acl_version: 1.5 }]) await expect(invalid("note", "note-id", "acl", payload)).rejects.toThrow("Invalid collaboration ACL version");
  expect(calls).toBe(0);
});
