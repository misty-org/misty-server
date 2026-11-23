import * as Y from "yjs";

import { jsonResponse } from "./control_protocol";
import { MAX_DOCUMENT_BYTES } from "./document_persistence";
import {
  burnTicketID,
  ticketErrorCode,
  verifyRoomTicket,
} from "./room_ticket_auth";
import type { Env } from "./room_env";
import type { RoomState } from "./room_state";
import type { CollaborationResourceType } from "./ticket";

interface RoomExportContext {
  env: Env;
  roomName: string;
  resourceType: CollaborationResourceType;
  state: RoomState;
  storage: DurableObjectStorage;
  document: Y.Doc;
}

export async function exportDocumentUpdate(
  request: Request,
  room: RoomExportContext,
): Promise<Response> {
  if (room.state.purged) {
    return jsonResponse({ code: "resource_deleted" }, 410);
  }
  const url = new URL(request.url);
  if (url.searchParams.get("export") !== "1") {
    return jsonResponse({ code: "not_found" }, 404);
  }
  let claims;
  try {
    claims = await verifyRoomTicket(
      room.env,
      room.roomName,
      room.resourceType,
      url.searchParams.get("ticket") ?? "",
    );
  } catch (error) {
    return jsonResponse({ code: ticketErrorCode(error) }, 401);
  }
  if (claims.acl_version < room.state.aclVersion) {
    return jsonResponse({ code: "ticket_acl_stale" }, 401);
  }
  if (!(await burnTicketID(room.storage, claims.jti))) {
    return jsonResponse({ code: "ticket_replayed" }, 401);
  }
  const update = Y.encodeStateAsUpdate(room.document);
  if (update.byteLength > MAX_DOCUMENT_BYTES) {
    return jsonResponse({ code: "document_too_large" }, 413);
  }
  return new Response(update, {
    status: 200,
    headers: {
      "Access-Control-Allow-Origin": "*",
      "Cache-Control": "private, no-store",
      "Content-Type": "application/vnd.yjs.update",
      "Content-Length": String(update.byteLength),
      "X-Content-Type-Options": "nosniff",
    },
  });
}
