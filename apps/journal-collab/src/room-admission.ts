import { roomIsFull } from "./room-policy";
import type { Env } from "./room-env";
import type { RoomState } from "./room-state";
import {
  burnTicketID,
  ticketErrorCode,
  verifyRoomTicket,
} from "./room-ticket-auth";
import type { CollaborationResourceType, TicketClaims } from "./ticket";

export interface AdmissionRoom {
  env: Env;
  roomName: string;
  resourceType: CollaborationResourceType;
  state: RoomState;
  storage: DurableObjectStorage;
  connectionCount(): number;
}

/**
 * Decides whether a socket may join the room. Returns the close code to refuse
 * with, or the verified ticket claims to admit it under.
 *
 * Order matters: the ticket id is burned before the capacity check, so a
 * refused-for-capacity ticket is still single use.
 */
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

  // A ticket older than the room's known ACL version is stale by definition:
  // permissions changed after it was minted, so the client must ask the API
  // for a fresh one that reflects the new grant set.
  if (claims.acl_version < room.state.aclVersion) {
    return { refusal: "ticket_acl_stale" };
  }
  if (claims.acl_version > room.state.aclVersion) {
    room.state.aclVersion = claims.acl_version;
    await room.storage.put("aclVersion", room.state.aclVersion);
  }

  // Single use. The check and the write are one storage transaction so two
  // simultaneous connections cannot both claim the same ticket.
  if (!(await burnTicketID(room.storage, claims.jti))) {
    return { refusal: "ticket_replayed" };
  }

  if (roomIsFull(room.connectionCount())) {
    return { refusal: "room_full" };
  }
  return { claims };
}
