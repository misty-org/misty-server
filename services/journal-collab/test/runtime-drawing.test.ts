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
    const applied = await control(room, "drawing_scene_apply", {
      request_id: "runtime-drawing-apply-1",
      elements: [
        { id: "mcp_box", type: "rectangle", x: 40, y: 60, width: 180, height: 90 },
        { id: "mcp_label", type: "text", x: 70, y: 90, text: "Drawn through MCP" },
      ],
      scene: { viewBackgroundColor: "#f8f9fa" },
    });
    assert.equal(applied.changed, 2);
    assert.equal(applied.element_count, 2);
    await waitFor(() => drawing.getMap("drawing:elements").has("mcp_box"));
    const read = await control(room, "drawing_scene_read", {});
    assert.equal(read.elements.length, 2);
    assert.equal(read.scene.viewBackgroundColor, "#f8f9fa");
    assert.equal(read.content_hash, applied.content_hash);
    const replay = await control(room, "drawing_scene_apply", {
      request_id: "runtime-drawing-apply-1",
      elements: [{ id: "ignored", type: "ellipse", x: 0, y: 0 }],
    });
    assert.equal(replay.replayed, true);
    assert.equal(replay.content_hash, applied.content_hash);
    const conflict = await controlResponse(room, "drawing_scene_apply", {
      base_hash: "0".repeat(64),
      elements: [{ id: "conflict", type: "ellipse", x: 0, y: 0 }],
    });
    assert.equal(conflict.status, 409);
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
