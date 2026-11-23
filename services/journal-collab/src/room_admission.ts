import { roomIsFull } from "./room_policy";
import type { Env } from "./room_env";
import type { RoomState } from "./room_state";
import {
  burnTicketID,
  ticketErrorCode,
  verifyRoomTicket,
} from "./room_ticket_auth";
import type { CollaborationResourceType, TicketClaims } from "./ticket";

interface AdmissionRoom {
  env: Env;
  roomName: string;
  resourceType: CollaborationResourceType;
  state: RoomState;
  storage: DurableObjectStorage;
  connectionCount(): number;
  reserve(): void;
}

export async function admitSocket(
  room: AdmissionRoom,
  token: string,
): Promise<{ refusal: string } | { claims: TicketClaims }> {
  let claims;
  try {
    claims = await verifyRoomTicket(room.env, room.roomName, room.resourceType, token);
  } catch (error) {
    return { refusal: ticketErrorCode(error) };
  }

  if (claims.acl_version < room.state.aclVersion) {
    return { refusal: "ticket_acl_stale" };
  }
  if (claims.acl_version > room.state.aclVersion) {
    room.state.aclVersion = claims.acl_version;
    await room.storage.put("aclVersion", room.state.aclVersion);
  }

  if (!(await burnTicketID(room.storage, claims.jti))) {
    return { refusal: "ticket_replayed" };
  }

  if (roomIsFull(room.connectionCount())) {
    return { refusal: "room_full" };
  }
  room.reserve();
  return { claims };
}
