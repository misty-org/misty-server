import * as Y from "yjs";

import { jsonResponse } from "./control_protocol";
import { emptyDocumentBytes } from "./document_content";
import { readDocumentSnapshot } from "./document_persistence";
import {
  getHistoryVersion,
  listHistoryVersions,
  restoreHistoryVersion,
} from "./room_history_control";
import { bootstrapNote, replaceNoteMarkdown } from "./room_note_control";
import { PURGED_KEY, type RoomState } from "./room_state";
import type { CollaborationResourceType } from "./ticket";

export interface RoomControlContext {
  readonly document: Y.Doc;
  readonly storage: DurableObjectStorage;
  readonly state: RoomState;
  readonly resourceType: CollaborationResourceType;
  readonly keepsHistory: boolean;
  readonly supportsMarkdownBootstrap: boolean;
  save(): Promise<void>;
  publishNoteProjection(): Promise<void>;
  disconnectSupersededSockets(): void;
  disconnectUsers(userIDs: string[] | null): void;
  closeAllSockets(code: string): void;
}

export async function handleRoomControl(
  context: RoomControlContext,
  command: string,
  payload: Record<string, unknown>,
): Promise<Response> {
  if (context.state.purged && command !== "purge" && command !== "status") {
    return jsonResponse({ code: "resource_deleted" }, 410);
  }
  switch (command) {
    case "bootstrap":
      return bootstrapNote(context, payload);
    case "replace_markdown":
      return replaceNoteMarkdown(context, payload);
    case "history_list":
      return listHistoryVersions(context);
    case "history_get":
      return getHistoryVersion(context, payload);
    case "history_restore":
      return restoreHistoryVersion(context, payload);
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
  context.state.purged = true;
  context.closeAllSockets("resource_deleted");
  await context.storage.deleteAll();
  await context.storage.put(PURGED_KEY, true);
  context.state.aclVersion = 0;
  return jsonResponse({ ok: true, purged: true });
}
