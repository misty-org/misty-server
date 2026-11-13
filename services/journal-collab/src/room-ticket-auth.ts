import { claimTicketID } from "./room-policy";
import type { Env } from "./room-env";
import {
  TicketError,
  verifyTicketWithRotation,
  type CollaborationResourceType,
} from "./ticket";

/** Used ticket ids are kept a little past ticket expiry, then swept. */
const JTI_RETENTION_MS = 5 * 60 * 1000;

/** Verifies a ticket against this room's key, issuer, audience and resource type. */
export async function verifyRoomTicket(
  env: Env,
  roomName: string,
  resourceType: CollaborationResourceType,
  token: string,
) {
  return verifyTicketWithRotation(
    token,
    {
      publicKeyBase64: env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY,
      issuer: env.JOURNAL_COLLAB_ISSUER,
      audience: env.JOURNAL_COLLAB_AUDIENCE,
      room: roomName,
      resourceType,
    },
    env.JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS,
  );
}

/** The client-facing reason for a failed ticket check. */
export function ticketErrorCode(error: unknown): string {
  return error instanceof TicketError ? error.code : "ticket_invalid";
}

/**
 * Records a ticket id if it has not been seen. Returns false on replay.
 *
 * Durable Object storage is serialized per object, so this read-then-write
 * cannot interleave with another connection to the same room.
 */
export async function burnTicketID(
  storage: DurableObjectStorage,
  jti: string,
): Promise<boolean> {
  const now = Date.now();
  if (!(await claimTicketID(storage, jti, now))) return false;
  // Opportunistic sweep so used ids do not accumulate forever.
  if (Math.random() < 0.05) await sweepTicketIDs(storage, now);
  return true;
}

async function sweepTicketIDs(storage: DurableObjectStorage, now: number): Promise<void> {
  const entries = await storage.list<number>({
    prefix: "jti:",
    limit: 200,
  });
  const stale: string[] = [];
  for (const [key, storedAt] of entries) {
    if (now - storedAt > JTI_RETENTION_MS) stale.push(key);
  }
  if (stale.length > 0) await storage.delete(stale);
}
