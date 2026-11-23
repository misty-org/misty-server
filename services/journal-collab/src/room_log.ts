import type { CollaborationResourceType } from "./ticket";

interface RoomLogIdentity {
  correlationID: string;
  resourceType: CollaborationResourceType;
}

export function shortRoomHash(name: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < name.length; index += 1) {
    hash ^= name.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

export function roomCorrelationID(resourceType: CollaborationResourceType, name: string): string {
  return `room_${resourceType}_${shortRoomHash(name)}`;
}

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
