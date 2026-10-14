import type { CollaborationResourceType } from "./ticket";

export interface RoomLogIdentity {
  correlationID: string;
  resourceType: CollaborationResourceType;
}

/**
 * Emits one JSON log line to the Worker logs. Details must never include
 * document content or member identities.
 */
export function logRoomEvent(
  identity: RoomLogIdentity,
  event: string,
  details: Record<string, unknown>,
): void {
  console.error(
    JSON.stringify({
      level: event.endsWith("failed") || event.endsWith("exceeded") ? "error" : "warn",
      event,
      correlation_id: identity.correlationID,
      resource_type: identity.resourceType,
      ...details,
    }),
  );
}
