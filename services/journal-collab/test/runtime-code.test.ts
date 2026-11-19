// This file intentionally exercises the emitted Worker bundle as a black box.
// Miniflare's runtime objects are more dynamic than the Worker source types.
// eslint-disable-next-line @typescript-eslint/ban-ts-comment -- black-box test of the built Worker
// @ts-nocheck
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "vitest";

import * as Y from "yjs";

import {
  control,
  controlResponse,
  createRuntime,
  mintTicket,
  providerFor,
  providerSynced,
  roomStub,
  waitFor,
} from "./runtime-harness";

function closeEvents(provider) {
  const events = [];
  provider.on("connection-close", (event) => {
    if (event) events.push({ code: event.code, reason: event.reason });
  });
  return events;
}

function deletedClose(events) {
  return events.some(
    (event) => event.code === 1008 && event.reason === "resource_deleted",
  );
}

test("CodeRoom shares files between editors, persists them, and closes as deleted on purge", async () => {
  const persistence = await mkdtemp(path.join(os.tmpdir(), "misty-code-room-"));
  let runtime;
  let providerA;
  let providerB;
  let restored;
  let afterPurge;
  try {
    runtime = createRuntime(persistence);
    const runtimeUrl = await runtime.ready;
    const roomName = "runtime-code-session";

    const docA = new Y.Doc({ gc: false });
    providerA = providerFor(runtimeUrl, roomName, docA, mintTicket(roomName, "dev-a", "code"), "code-room");
    await providerSynced(providerA);
    const docB = new Y.Doc({ gc: false });
    providerB = providerFor(runtimeUrl, roomName, docB, mintTicket(roomName, "dev-b", "code"), "code-room");
    await providerSynced(providerB);

    // Every member edits: both sides write, and both see both edits.
    const fileA = new Y.Text("export const answer = 1;\n");
    docA.getMap("files").set("src/answer.ts", fileA);
    await waitFor(() => docB.getMap("files").has("src/answer.ts"));
    docB.getMap("files").get("src/answer.ts").insert(0, "// shared\n");
    docB.getArray("steps").push([{ id: "step-1", path: "src/answer.ts", summary: "Add a comment" }]);
    await waitFor(
      () =>
        docA.getMap("files").get("src/answer.ts").toString() ===
          "// shared\nexport const answer = 1;\n" && docA.getArray("steps").length === 1,
    );

    // Saved after the debounce, and restored after a restart.
    await new Promise((resolve) => setTimeout(resolve, 2_300));
    const room = await roomStub(runtime, "CODE_ROOM", roomName);
    const saved = await control(room, "status", {});
    assert.equal(saved.persistence_source, "current");
    assert.ok(saved.persisted_bytes > 2);
    providerA.destroy();
    providerA = undefined;
    providerB.destroy();
    providerB = undefined;
    await runtime.dispose();

    runtime = createRuntime(persistence);
    const restoredUrl = await runtime.ready;
    const restoredDoc = new Y.Doc({ gc: false });
    restored = providerFor(restoredUrl, roomName, restoredDoc, mintTicket(roomName, "dev-a", "code"), "code-room");
    const restoredCloses = closeEvents(restored);
    await providerSynced(restored);
    await waitFor(
      () =>
        restoredDoc.getMap("files").get("src/answer.ts")?.toString() ===
        "// shared\nexport const answer = 1;\n",
    );

    // The code room has no note or drawing commands.
    const restartedRoom = await roomStub(runtime, "CODE_ROOM", roomName);
    for (const [command, payload] of [
      ["bootstrap", { title: "Not a note", markdown: "ignored" }],
      ["replace_markdown", { title: "Not a note", markdown: "ignored" }],
      ["drawing_scene_read", {}],
    ]) {
      const response = await controlResponse(restartedRoom, command, payload);
      assert.equal(response.status, 400);
      assert.deepEqual(await response.json(), { code: "unknown_command" });
    }

    // Ending the session purges the room: open sockets close as deleted, its state goes.
    assert.deepEqual(await control(restartedRoom, "purge", {}), { ok: true, purged: true });
    await waitFor(() => deletedClose(restoredCloses));
    restored.destroy();
    restored = undefined;
    const purged = await control(restartedRoom, "status", {});
    assert.equal(purged.purged, true);
    assert.equal(purged.persisted_bytes, 0);

    // A ticket minted before the end cannot reopen it.
    afterPurge = providerFor(restoredUrl, roomName, new Y.Doc(), mintTicket(roomName, "dev-b", "code"), "code-room");
    const afterPurgeCloses = closeEvents(afterPurge);
    await waitFor(() => deletedClose(afterPurgeCloses));
  } finally {
    providerA?.destroy();
    providerB?.destroy();
    restored?.destroy();
    afterPurge?.destroy();
    if (runtime) await runtime.dispose();
    await rm(persistence, { recursive: true, force: true });
  }
});

test("CodeRoom admits only code tickets", async () => {
  const runtime = createRuntime();
  let provider;
  try {
    const runtimeUrl = await runtime.ready;
    const roomName = "runtime-code-wrong-ticket";
    provider = providerFor(runtimeUrl, roomName, new Y.Doc(), mintTicket(roomName, "dev-a", "drawing"), "code-room");
    const closes = closeEvents(provider);
    await waitFor(() =>
      closes.some((event) => event.code === 1008 && event.reason === "ticket_resource_mismatch"),
    );
  } finally {
    provider?.destroy();
    await runtime.dispose();
  }
});
