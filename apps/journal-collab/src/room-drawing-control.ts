import * as Y from "yjs";

import { jsonResponse } from "./control-protocol";
import { MAX_DOCUMENT_BYTES } from "./document-persistence";
import {
  applyDrawingSceneMutation,
  buildDrawingSceneMutation,
  drawingSceneHash,
  drawingSceneState,
} from "./drawing-scene";
import type { RoomControlContext } from "./room-control";

const DRAWING_REQUESTS_KEY = "drawingRequests";
const MAX_DRAWING_REQUESTS = 200;

/** Returns the scene with its current revision and content hash. */
export async function readDrawingScene(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (!context.supportsDrawingScene) return jsonResponse({ code: "unknown_command" }, 400);
  const revision = (await context.storage.get<number>("drawingRevision")) ?? 0;
  return jsonResponse({
    ok: true,
    revision,
    content_hash: await drawingSceneHash(context.document),
    ...drawingSceneState(context.document, payload.include_deleted === true),
  });
}

/**
 * Applies a scene mutation after checking the base hash. Mutations are
 * rehearsed on a copy first so an oversized result never reaches the room.
 */
export async function applyDrawingScene(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (!context.supportsDrawingScene) return jsonResponse({ code: "unknown_command" }, 400);
  const requestID = typeof payload.request_id === "string" ? payload.request_id.trim() : "";
  if (requestID && (requestID.length > 200 || !/^[A-Za-z0-9:._-]+$/u.test(requestID))) {
    return jsonResponse({ code: "invalid_request_id" }, 400);
  }
  if (requestID) {
    const requests = (await context.storage.get<Record<string, Record<string, unknown>>>(DRAWING_REQUESTS_KEY)) ?? {};
    const replay = requests[requestID];
    if (replay) return jsonResponse({ ...replay, replayed: true });
  }
  const currentHash = await drawingSceneHash(context.document);
  if (typeof payload.base_hash === "string" && payload.base_hash !== currentHash) {
    return jsonResponse({ code: "drawing_conflict", content_hash: currentHash }, 409);
  }
  let mutation;
  try {
    mutation = buildDrawingSceneMutation(context.document, payload);
  } catch (error) {
    return jsonResponse({ code: error instanceof Error ? error.message : "invalid_drawing_scene" }, 400);
  }
  const projected = new Y.Doc();
  Y.applyUpdate(projected, Y.encodeStateAsUpdate(context.document));
  applyDrawingSceneMutation(projected, mutation);
  const projectedBytes = Y.encodeStateAsUpdate(projected).byteLength;
  projected.destroy();
  if (projectedBytes > MAX_DOCUMENT_BYTES) return jsonResponse({ code: "document_too_large" }, 413);
  applyDrawingSceneMutation(context.document, mutation);
  await context.save();
  const revision = ((await context.storage.get<number>("drawingRevision")) ?? 0) + 1;
  const result = {
    ok: true,
    revision,
    content_hash: await drawingSceneHash(context.document),
    changed: mutation.changed,
    deleted: mutation.deleted,
    element_count: drawingSceneState(context.document, false).elements instanceof Array
      ? (drawingSceneState(context.document, false).elements as unknown[]).length
      : 0,
  };
  await context.storage.put("drawingRevision", revision);
  if (requestID) {
    const requests = (await context.storage.get<Record<string, Record<string, unknown>>>(DRAWING_REQUESTS_KEY)) ?? {};
    requests[requestID] = result;
    const ids = Object.keys(requests);
    for (const id of ids.slice(0, Math.max(0, ids.length - MAX_DRAWING_REQUESTS))) delete requests[id];
    await context.storage.put(DRAWING_REQUESTS_KEY, requests);
  }
  return jsonResponse(result);
}
