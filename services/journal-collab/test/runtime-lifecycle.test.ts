// This file intentionally exercises the emitted Worker bundle as a black box.
// Miniflare's runtime objects are more dynamic than the Worker source types.
// eslint-disable-next-line @typescript-eslint/ban-ts-comment -- black-box test of the built Worker
// @ts-nocheck
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "vitest";

import * as Y from "yjs";

import {
  awarenessHasUser,
  bindings,
  control,
  controlResponse,
  createRuntime,
  mintTicket,
  providerFor,
  providerSynced,
  roomStub,
  waitFor,
} from "./runtime-harness";

test("service bootstrap publishes note content before any editor connects and recovers old bindings", async () => {
  const projections = [];
  const runtime = createRuntime(undefined, {outboundService: async request => {
    assert.equal(request.url,"https://api.invalid/internal/journal/note-projections");
    const body = await request.text();
    const signature = createHmac("sha256",Buffer.from(bindings.JOURNAL_COLLAB_PROJECTION_SECRET,"base64"))
      .update(`${request.headers.get("X-Misty-Timestamp")}\n`).update(body).digest("base64url");
    assert.equal(request.headers.get("X-Misty-Signature"),signature);
    projections.push(JSON.parse(body));
    return new Response("{}");
  }});
  try {
    const room = await roomStub(runtime,"NOTE_ROOM","unopened-note");
    const payload = {title:"Agent research",markdown:"Verified source: https://excalidraw.com"};
    let response = await controlResponse(room,"bootstrap",payload,"note-unopened");
    assert.equal(response.status,200);
    assert.ok(projections.some(item => item.note_id === "note-unopened" && item.markdown === payload.markdown));
    response = await controlResponse(room,"bootstrap",{...payload,markdown:"Must not overwrite"},"note-unopened");
    assert.deepEqual(await response.json(),{ok:true,initialized:false});
    assert.equal(projections.at(-1).markdown,payload.markdown);
    response = await controlResponse(room,"bootstrap",payload,"note-other");
    assert.equal(response.status,409);
    const historical = await roomStub(runtime,"NOTE_ROOM","historical-note");
    await control(historical,"bootstrap",payload);
    const before = projections.length;
    response = await controlResponse(historical,"bootstrap",payload,"note-historical");
    assert.equal(response.status,200);
    assert.equal(projections.length,before+1);
    assert.equal(projections.at(-1).note_id,"note-historical");
    assert.equal(projections.at(-1).markdown,payload.markdown);
  } finally {
    await runtime.dispose();
  }
});

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
      exportedDocument.getText("misty:markdown").toString(),
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
    assert.equal(purged.purged, true);

    const staleExport = await purgedRoom.fetch(
      `http://room.internal/?export=1&ticket=${encodeURIComponent(mintTicket("runtime-note", "user-after-purge"))}`,
    );
    assert.equal(staleExport.status, 410);
    assert.deepEqual(await staleExport.json(), { code: "resource_deleted" });

    const staleBootstrap = await controlResponse(purgedRoom, "bootstrap", {
      title: "Must not return",
      markdown: "A delayed command cannot recreate deleted content.",
    });
    assert.equal(staleBootstrap.status, 410);
    assert.deepEqual(await staleBootstrap.json(), { code: "resource_deleted" });

    // A save may already be queued when purge starts. It must not recreate the
    // deleted snapshot after the purge response has succeeded.
    await new Promise((resolve) => setTimeout(resolve, 2_300));
    await runtime.dispose();
    runtime = createRuntime(persistence);
    const afterPurgeRestart = await roomStub(runtime, "NOTE_ROOM", "runtime-note");
    const afterPurge = await control(afterPurgeRestart, "status", {});
    assert.ok(afterPurge.document_bytes < restored.document_bytes);
    assert.ok(afterPurge.persisted_bytes < restored.persisted_bytes);
    assert.equal(afterPurge.purged, true);
    assert.deepEqual(await control(afterPurgeRestart, "purge", {}), {
      ok: true,
      purged: true,
    });
  } finally {
    if (runtime) await runtime.dispose();
    await rm(persistence, { recursive: true, force: true });
  }
});
