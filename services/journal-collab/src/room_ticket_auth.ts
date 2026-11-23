import { claimTicketID } from "./room_policy";
import type { Env } from "./room_env";
import {
  TicketError,
  verifyTicketWithRotation,
  type CollaborationResourceType,
} from "./ticket";

const JTI_RETENTION_MS = 5 * 60 * 1000;

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

export function ticketErrorCode(error: unknown): string {
  return error instanceof TicketError ? error.code : "ticket_invalid";
}

export async function burnTicketID(
  storage: DurableObjectStorage,
  jti: string,
): Promise<boolean> {
  const now = Date.now();
  if (!(await claimTicketID(storage, jti, now))) return false;
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
