import * as Y from "yjs";

import { jsonResponse } from "./control-protocol";
import { emptyDocumentBytes } from "./document-content";
import { readDocumentSnapshot } from "./document-persistence";
import { applyDrawingScene, readDrawingScene } from "./room-drawing-control";
import { bootstrapNote, replaceNoteMarkdown } from "./room-note-control";
import { PURGED_KEY, type RoomState } from "./room-state";

/** Everything a control command may read or change in its room. */
export interface RoomControlContext {
  readonly document: Y.Doc;
  readonly storage: DurableObjectStorage;
  readonly state: RoomState;
  readonly supportsMarkdownBootstrap: boolean;
  readonly supportsDrawingScene: boolean;
  save(): Promise<void>;
  publishNoteProjection(): Promise<void>;
  disconnectSupersededSockets(): void;
  disconnectUsers(userIDs: string[] | null): void;
}

/** Handles authenticated control commands from the Go API. */
export async function handleRoomControl(
  context: RoomControlContext,
  command: string,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (context.state.purged && command !== "purge" && command !== "status") {
    return jsonResponse({ code: "resource_deleted" }, 410);
  }
  switch (command) {
    case "drawing_scene_read":
      return readDrawingScene(context, payload);
    case "drawing_scene_apply":
      return applyDrawingScene(context, payload);
    case "bootstrap":
      return bootstrapNote(context, payload);
    case "replace_markdown":
      return replaceNoteMarkdown(context, payload);
    case "acl":
      return setACLVersion(context, payload);
    case "disconnect": {
      const userIDs = Array.isArray(payload.user_ids)
        ? (payload.user_ids as string[])
        : null;
      context.disconnectUsers(userIDs);
      return jsonResponse({ ok: true });
    }
    case "status":
      return roomStatus(context);
    case "purge":
      return purgeRoom(context);
    default:
      return jsonResponse({ code: "unknown_command" }, 400);
  }
}

async function setACLVersion(
  context: RoomControlContext,
  payload: Record<string, unknown>,
): Promise<Response> {
  const version = Number(payload.acl_version);
  if (!Number.isInteger(version) || version < 1) {
    return jsonResponse({ code: "invalid_acl_version" }, 400);
  }
  // Only ever moves forward, so an out-of-order retry of an older
  // command cannot re-admit users who were already revoked.
  if (version > context.state.aclVersion) {
    context.state.aclVersion = version;
    await context.storage.put("aclVersion", version);
  }
  context.disconnectSupersededSockets();
  return jsonResponse({ ok: true, acl_version: context.state.aclVersion });
}

async function roomStatus(context: RoomControlContext): Promise<Response> {
  const persisted = await readDocumentSnapshot(context.storage);
  return jsonResponse({
    ok: true,
    acl_version: context.state.aclVersion,
    document_bytes: context.state.purged
      ? emptyDocumentBytes()
      : Y.encodeStateAsUpdate(context.document).byteLength,
    persisted_bytes: persisted?.update.byteLength ?? 0,
    persistence_source: persisted?.source ?? "empty",
    purged: context.state.purged,
  });
}

async function purgeRoom(context: RoomControlContext): Promise<Response> {
  // Keep only a tombstone. It blocks already-minted tickets and delayed
  // callbacks without retaining any document content.
  context.disconnectUsers(null);
  context.state.purged = true;
  await context.storage.deleteAll();
  await context.storage.put(PURGED_KEY, true);
  context.state.aclVersion = 0;
  return jsonResponse({ ok: true, purged: true });
}
