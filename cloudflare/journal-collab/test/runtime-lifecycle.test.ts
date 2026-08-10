// This file intentionally exercises the emitted Worker bundle as a black box.
// Miniflare's runtime objects are more dynamic than the Worker source types.
// @ts-nocheck
import assert from "node:assert/strict";
import {
  createHmac,
  generateKeyPairSync,
  randomBytes,
  sign,
} from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "vitest";

import { Miniflare } from "miniflare";
import YProvider from "y-partyserver/provider";
import * as Y from "yjs";

const controlSecret = randomBytes(32);
const ticketKeys = generateKeyPairSync("ed25519");
const ticketPublicDer = ticketKeys.publicKey.export({ format: "der", type: "spki" });
const bindings = {
  JOURNAL_COLLAB_TICKET_PUBLIC_KEY: ticketPublicDer.subarray(-32).toString("base64"),
  JOURNAL_COLLAB_CONTROL_SECRET: controlSecret.toString("base64"),
  JOURNAL_COLLAB_PROJECTION_SECRET: randomBytes(32).toString("base64"),
  JOURNAL_COLLAB_ISSUER: "misty-api",
  JOURNAL_COLLAB_AUDIENCE: "misty-journal-collab",
  MISTY_INTERNAL_API_BASE: "https://api.invalid",
};

test("fresh tickets reconnect the same named collaborator without ghost presence", async () => {
  const runtime = createRuntime();
  let providerA;
  let providerB;
  let providerA2;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-reconnect";
    const docA = new Y.Doc();
    providerA = providerFor(runtimeUrl, room, docA, mintTicket(room, "user-a"));
    providerA.awareness.setLocalStateField("user", {
      id: "user-a",
      name: "Ada",
      color: { background: "#fff", stroke: "#000" },
    });
    await providerSynced(providerA);
    docA.getText("content").insert(0, "survives reconnect");

    const docB = new Y.Doc();
    providerB = providerFor(runtimeUrl, room, docB, mintTicket(room, "user-b"));
    providerB.awareness.setLocalStateField("user", {
      id: "user-b",
      name: "Ben",
    });
    await providerSynced(providerB);
    await waitFor(() => docB.getText("content").toString() === "survives reconnect");
    await waitFor(() => awarenessHasUser(providerB, "user-a", "Ada"));

    providerA.destroy();
    providerA = undefined;
    await waitFor(() => !awarenessHasUser(providerB, "user-a", "Ada"));

    const reconnectedDoc = new Y.Doc();
    providerA2 = providerFor(
      runtimeUrl,
      room,
      reconnectedDoc,
      mintTicket(room, "user-a"),
    );
    providerA2.awareness.setLocalStateField("user", {
      id: "user-a",
      name: "Ada",
      color: { background: "#fff", stroke: "#000" },
    });
    await providerSynced(providerA2);
    await waitFor(
      () => reconnectedDoc.getText("content").toString() === "survives reconnect",
    );
    await waitFor(() => awarenessHasUser(providerB, "user-a", "Ada"));
  } finally {
    providerA?.destroy();
    providerA2?.destroy();
    providerB?.destroy();
    await runtime.dispose();
  }
});

test("NoteRoom survives restart and purge cannot resurrect in-memory content", async () => {
  const persistence = await mkdtemp(path.join(os.tmpdir(), "misty-note-room-"));
  let runtime;
  try {
    runtime = createRuntime(persistence);
    const room = await roomStub(runtime, "NOTE_ROOM", "runtime-note");

    const bootstrap = await control(room, "bootstrap", {
      title: "Runtime lifecycle",
      markdown: "Persist this collaborative note across eviction.",
    });
    assert.deepEqual(bootstrap, { ok: true, initialized: true });

    const saved = await control(room, "status", {});
    assert.equal(saved.persistence_source, "current");
    assert.ok(saved.document_bytes > 2);
    assert.equal(saved.persisted_bytes, saved.document_bytes);

    await runtime.dispose();
    runtime = createRuntime(persistence);
    const restarted = await roomStub(runtime, "NOTE_ROOM", "runtime-note");
    const restored = await control(restarted, "status", {});
    assert.equal(restored.persistence_source, "current");
    assert.equal(restored.document_bytes, saved.document_bytes);
    assert.equal(restored.persisted_bytes, saved.persisted_bytes);

    const exportTicket = mintTicket("runtime-note", "user-export");
    const exported = await restarted.fetch(
      `http://room.internal/?export=1&ticket=${encodeURIComponent(exportTicket)}`,
    );
    assert.equal(exported.status, 200);
    assert.equal(exported.headers.get("Access-Control-Allow-Origin"), "*");
    const exportedDocument = new Y.Doc();
    Y.applyUpdate(exportedDocument, new Uint8Array(await exported.arrayBuffer()));
    assert.equal(
      exportedDocument.getText("markdown").toString(),
      "Persist this collaborative note across eviction.",
    );
    const replay = await restarted.fetch(
      `http://room.internal/?export=1&ticket=${encodeURIComponent(exportTicket)}`,
    );
    assert.equal(replay.status, 401);
    assert.deepEqual(await replay.json(), { code: "ticket_replayed" });

    assert.deepEqual(await control(restarted, "purge", {}), {
      ok: true,
      purged: true,
    });
    await new Promise((resolve) => setTimeout(resolve, 100));
    const purgedRoom = await roomStub(runtime, "NOTE_ROOM", "runtime-note");
    const purged = await control(purgedRoom, "status", {});
    assert.ok(purged.document_bytes < restored.document_bytes);
    assert.equal(purged.persisted_bytes, 0);

    // The Yjs reset schedules the normal debounced save. If purge only deleted
    // storage without clearing memory, this wait would recreate the old note.
    await new Promise((resolve) => setTimeout(resolve, 2_300));
    await runtime.dispose();
    runtime = createRuntime(persistence);
    const afterPurgeRestart = await roomStub(runtime, "NOTE_ROOM", "runtime-note");
    const afterPurge = await control(afterPurgeRestart, "status", {});
    assert.ok(afterPurge.document_bytes < restored.document_bytes);
    assert.ok(afterPurge.persisted_bytes < restored.persisted_bytes);
    assert.deepEqual(await control(afterPurgeRestart, "purge", {}), {
      ok: true,
      purged: true,
    });
  } finally {
    if (runtime) await runtime.dispose();
    await rm(persistence, { recursive: true, force: true });
  }
});

test("DrawingRoom saves, restarts, reconnects, and purges a scene", async () => {
  const persistence = await mkdtemp(path.join(os.tmpdir(), "misty-drawing-room-"));
  let runtime;
  let provider;
  let reconnected;
  try {
    runtime = createRuntime(persistence);
    const runtimeUrl = await runtime.ready;
    const roomName = "runtime-drawing-persisted";
    const drawing = new Y.Doc();
    provider = providerFor(
      runtimeUrl,
      roomName,
      drawing,
      mintTicket(roomName, "artist-a", "drawing"),
      "drawing-room",
    );
    await providerSynced(provider);
    drawing.getMap("drawing:scene").set("backgroundColor", "#fafafa");
    await new Promise((resolve) => setTimeout(resolve, 2_300));

    const room = await roomStub(runtime, "DRAWING_ROOM", roomName);
    const saved = await control(room, "status", {});
    assert.equal(saved.persistence_source, "current");
    assert.ok(saved.persisted_bytes > 2);
    provider.destroy();
    provider = undefined;

    await runtime.dispose();
    runtime = createRuntime(persistence);
    const restoredRoom = await roomStub(runtime, "DRAWING_ROOM", roomName);
    const restored = await control(restoredRoom, "status", {});
    assert.equal(restored.document_bytes, saved.document_bytes);

    const restoredUrl = await runtime.ready;
    const restoredDrawing = new Y.Doc();
    reconnected = providerFor(
      restoredUrl,
      roomName,
      restoredDrawing,
      mintTicket(roomName, "artist-a", "drawing"),
      "drawing-room",
    );
    await providerSynced(reconnected);
    await waitFor(
      () =>
        restoredDrawing.getMap("drawing:scene").get("backgroundColor") ===
        "#fafafa",
    );
    reconnected.destroy();
    reconnected = undefined;

    assert.deepEqual(await control(restoredRoom, "purge", {}), {
      ok: true,
      purged: true,
    });
  } finally {
    provider?.destroy();
    reconnected?.destroy();
    if (runtime) await runtime.dispose();
    await rm(persistence, { recursive: true, force: true });
  }
});

test("DrawingRoom starts empty and rejects note-only bootstrap", async () => {
  const runtime = createRuntime();
  try {
    const room = await roomStub(runtime, "DRAWING_ROOM", "runtime-drawing");
    const status = await control(room, "status", {});
    assert.equal(status.persistence_source, "empty");
    assert.equal(status.persisted_bytes, 0);

    const response = await controlResponse(room, "bootstrap", {
      title: "Not a drawing command",
      markdown: "ignored",
    });
    assert.equal(response.status, 400);
    assert.deepEqual(await response.json(), { code: "unknown_command" });
    assert.deepEqual(await control(room, "purge", {}), {
      ok: true,
      purged: true,
    });
  } finally {
    await runtime.dispose();
  }
});

function createRuntime(persistence) {
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
  });
}

function providerFor(runtimeUrl, room, doc, ticket, party = "note-room") {
  return new YProvider(runtimeUrl.host, room, doc, {
    party,
    protocol: "ws",
    disableBc: true,
    WebSocketPolyfill: WebSocket,
    params: { ticket },
  });
}

function providerSynced(provider) {
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

function awarenessHasUser(provider, id, name) {
  return [...provider.awareness.getStates().values()].some(
    (state) => state.user?.id === id && state.user?.name === name,
  );
}

async function waitFor(predicate) {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("runtime collaboration condition timed out");
}

function mintTicket(room, userID, resourceType = "note") {
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

async function roomStub(runtime, binding, name) {
  const namespace = await runtime.getDurableObjectNamespace(binding);
  return namespace.get(namespace.idFromName(name));
}

async function control(room, command, payload) {
  const response = await controlResponse(room, command, payload);
  if (response.status !== 200) {
    throw new Error(`control command returned ${response.status}: ${await response.text()}`);
  }
  return response.json();
}

async function controlResponse(room, command, payload) {
  const body = JSON.stringify({ command, payload });
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
