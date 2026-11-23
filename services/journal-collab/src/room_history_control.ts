import * as Y from "yjs";

import { jsonResponse } from "./control_protocol";
import { listHistory, readHistoryVersion, recordHistoryNow } from "./document_history";
import { MAX_DOCUMENT_BYTES } from "./document_persistence";
import { restoreDocument } from "./document_restore";
import type { RoomControlContext } from "./room_control";
import { readRoomLimits } from "./room_limits";

const VERSION_ID = /^[A-Za-z0-9_-]{1,64}$/u;

function versionID(payload: Record<string, unknown>): string | null {
  const id = typeof payload.version_id === "string" ? payload.version_id : "";
  return VERSION_ID.test(id) ? id : null;
}

function base64(bytes: Uint8Array): string {
  let binary = "";
  const step = 0x8000;
  for (let offset = 0; offset < bytes.byteLength; offset += step) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + step));
  }
  return btoa(binary);
}

export async function listHistoryVersions(context: RoomControlContext): Promise<Response> {
  if (!context.keepsHistory) return jsonResponse({ code: "unknown_command" }, 400);
  const entries = await listHistory(context.storage);
  return jsonResponse({
    ok: true,
    versions: entries.reverse().map((entry) => ({
      id: entry.id,
      created_at: new Date(entry.createdAt).toISOString(),
      content_bytes: entry.byteLength,
    })),
  });
}

export async function getHistoryVersion(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (!context.keepsHistory) return jsonResponse({ code: "unknown_command" }, 400);
  const id = versionID(payload);
  if (!id) return jsonResponse({ code: "invalid_version" }, 400);
  const version = await readHistoryVersion(context.storage, id);
  if (!version) return jsonResponse({ code: "version_not_found" }, 404);
  return jsonResponse({
    ok: true,
    id: version.entry.id,
    created_at: new Date(version.entry.createdAt).toISOString(),
    content_bytes: version.entry.byteLength,
    update: base64(version.update),
  });
}

export async function restoreHistoryVersion(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (!context.keepsHistory) return jsonResponse({ code: "unknown_command" }, 400);
  const id = versionID(payload);
  if (!id) return jsonResponse({ code: "invalid_version" }, 400);
  const version = await readHistoryVersion(context.storage, id);
  if (!version) return jsonResponse({ code: "version_not_found" }, 404);

  const now = Date.now();
  const rehearsal = new Y.Doc();
  try {
    Y.applyUpdate(rehearsal, Y.encodeStateAsUpdate(context.document));
    restoreDocument(rehearsal, version.update, context.resourceType);
    if (Y.encodeStateAsUpdate(rehearsal).byteLength > MAX_DOCUMENT_BYTES) {
      return jsonResponse({ code: "document_too_large" }, 413);
    }
  } finally {
    rehearsal.destroy();
  }

  const limits = await readRoomLimits(context.storage);
  await recordHistoryNow(context.storage, Y.encodeStateAsUpdate(context.document), now, limits.historyDays);
  restoreDocument(context.document, version.update, context.resourceType);
  await context.save();
  return jsonResponse({ ok: true, restored: id });
}
