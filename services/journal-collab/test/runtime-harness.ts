// Helpers for the runtime lifecycle test, which drives the emitted Worker bundle as a black box.
// Miniflare's runtime objects are more dynamic than the Worker source types.
// eslint-disable-next-line @typescript-eslint/ban-ts-comment -- black-box test of the built Worker
// @ts-nocheck
import {
  createHmac,
  generateKeyPairSync,
  randomBytes,
  sign,
} from "node:crypto";
import path from "node:path";

import { Miniflare } from "miniflare";
import YProvider from "y-partyserver/provider";

const controlSecret = randomBytes(32);
const ticketKeys = generateKeyPairSync("ed25519");
const ticketPublicDer = ticketKeys.publicKey.export({ format: "der", type: "spki" });
export const bindings = {
  JOURNAL_COLLAB_TICKET_PUBLIC_KEY: ticketPublicDer.subarray(-32).toString("base64"),
  JOURNAL_COLLAB_CONTROL_SECRET: controlSecret.toString("base64"),
  JOURNAL_COLLAB_PROJECTION_SECRET: randomBytes(32).toString("base64"),
  JOURNAL_COLLAB_ISSUER: "misty-api",
  JOURNAL_COLLAB_AUDIENCE: "misty-journal-collab",
  MISTY_INTERNAL_API_BASE: "https://api.invalid",
};

export function createRuntime(persistence, options = {}) {
  return new Miniflare({
    modules: true,
    scriptPath: path.resolve("dist/index.js"),
    compatibilityDate: "2026-06-25",
    compatibilityFlags: ["nodejs_compat"],
    durableObjects: {
      NOTE_ROOM: { className: "NoteRoom", useSQLite: true },
      DRAWING_ROOM: { className: "DrawingRoom", useSQLite: true },
    },
    durableObjectsPersist: persistence,
    bindings,
    ...options,
  });
}

export function providerFor(runtimeUrl, room, doc, ticket, party = "note-room") {
  return new YProvider(runtimeUrl.host, room, doc, {
    party,
    protocol: "ws",
    disableBc: true,
    WebSocketPolyfill: WebSocket,
    params: { ticket },
  });
}

export function providerSynced(provider) {
  if (provider.synced) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      provider.off("synced", onSynced);
      reject(new Error("collaboration provider did not synchronize"));
    }, 5_000);
    const onSynced = (synced) => {
      if (!synced) return;
      clearTimeout(timeout);
      provider.off("synced", onSynced);
      resolve();
    };
    provider.on("synced", onSynced);
  });
}

export function awarenessHasUser(provider, id, name) {
  return [...provider.awareness.getStates().values()].some(
    (state) => state.user?.id === id && state.user?.name === name,
  );
}

export async function waitFor(predicate) {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("runtime collaboration condition timed out");
}

export function mintTicket(room, userID, resourceType = "note") {
  const resourceID =
    resourceType === "drawing" ? "drawing-runtime" : "note-runtime";
  const header = base64Url(JSON.stringify({ alg: "EdDSA", typ: "JWT" }));
  const payload = base64Url(
    JSON.stringify({
      iss: "misty-api",
      aud: "misty-journal-collab",
      jti: `ticket-${randomBytes(12).toString("hex")}`,
      sub: userID,
      space_id: "space-runtime",
      resource_type: resourceType,
      resource_id: resourceID,
      ...(resourceType === "drawing"
        ? { drawing_id: resourceID }
        : { note_id: resourceID }),
      room,
      role: "editor",
      acl_version: 1,
      exp: Math.floor(Date.now() / 1_000) + 60,
    }),
  );
  const signature = sign(
    null,
    Buffer.from(`${header}.${payload}`),
    ticketKeys.privateKey,
  ).toString("base64url");
  return `${header}.${payload}.${signature}`;
}

function base64Url(value) {
  return Buffer.from(value).toString("base64url");
}

export async function roomStub(runtime, binding, name) {
  const namespace = await runtime.getDurableObjectNamespace(binding);
  return namespace.get(namespace.idFromName(name));
}

export async function control(room, command, payload) {
  const response = await controlResponse(room, command, payload);
  if (response.status !== 200) {
    throw new Error(`control command returned ${response.status}: ${await response.text()}`);
  }
  return response.json();
}

export async function controlResponse(room, command, payload, resourceID) {
  const body = JSON.stringify({ command, payload, ...(resourceID ? {resource_id:resourceID} : {}) });
  const timestamp = String(Math.floor(Date.now() / 1_000));
  const signature = createHmac("sha256", controlSecret)
    .update(`${timestamp}\n`)
    .update(body)
    .digest("base64url");
  return room.fetch("http://room.internal/control", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Misty-Timestamp": timestamp,
      "X-Misty-Signature": signature,
    },
    body,
  });
}
