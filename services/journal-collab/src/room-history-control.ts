import * as Y from "yjs";

import { jsonResponse } from "./control-protocol";
import { listHistory, readHistoryVersion, recordHistoryNow } from "./document-history";
import { MAX_DOCUMENT_BYTES } from "./document-persistence";
import { restoreDocument } from "./document-restore";
import type { RoomControlContext } from "./room-control";
import { readRoomLimits } from "./room-limits";

/**
 * The history commands the Go API sends after checking the caller may read the item (list, get)
 * or edit it (restore), and the rename it sends when a deck or workbook is renamed.
 */

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

/** Lists the kept versions, newest first. */
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

/** Returns one version as a full Yjs state, base64-encoded. */
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

/**
 * Makes a version the document's content again, as a new change everyone connected receives.
 * What it replaces is kept as a version first, so a restore can itself be undone. The change is
 * rehearsed on a copy, so a restore never pushes the room past its size limit.
 */
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
    restoreDocument(rehearsal, version.update, context.resourceType, now);
    if (Y.encodeStateAsUpdate(rehearsal).byteLength > MAX_DOCUMENT_BYTES) {
      return jsonResponse({ code: "document_too_large" }, 413);
    }
  } finally {
    rehearsal.destroy();
  }

  const limits = await readRoomLimits(context.storage);
  await recordHistoryNow(context.storage, Y.encodeStateAsUpdate(context.document), now, limits.historyDays);
  restoreDocument(context.document, version.update, context.resourceType, now);
  await context.save();
  return jsonResponse({ ok: true, restored: id });
}

/** Sets a deck's or workbook's title in its document, after a rename through the API. */
export async function setItemTitle(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  const metaName =
    context.resourceType === "deck" ? "deck:meta" : context.resourceType === "workbook" ? "wb:meta" : null;
  if (!metaName) return jsonResponse({ code: "unknown_command" }, 400);
  const title = typeof payload.title === "string" ? payload.title.trim() : "";
  if (!title || [...title].length > 200) return jsonResponse({ code: "invalid_title" }, 400);
  const meta = context.document.getMap<unknown>(metaName);
  if (meta.get("title") !== title) {
    context.document.transact(() => meta.set("title", title), "misty:title-control");
    await context.save();
  }
  return jsonResponse({ ok: true });
}
