import type { DocumentRole } from "./ticket";

export interface DocumentSocketState {
  userID: string;
  role: DocumentRole;
  aclVersion: number;
  resourceID: string;
  spaceID: string;
}

export const MAX_CONNECTIONS_PER_ROOM = 40;
export const MAX_MESSAGE_BYTES = 512 * 1024;

export function socketState(value: unknown): DocumentSocketState | null {
  if (
    typeof value !== "object" ||
    value === null ||
    typeof (value as DocumentSocketState).userID !== "string" ||
    !["creator", "editor", "viewer"].includes(
      (value as DocumentSocketState).role,
    ) ||
    typeof (value as DocumentSocketState).aclVersion !== "number" ||
    typeof (value as DocumentSocketState).resourceID !== "string" ||
    typeof (value as DocumentSocketState).spaceID !== "string"
  ) {
    return null;
  }
  return value as DocumentSocketState;
}

export function socketIsReadOnly(
  value: unknown,
  currentACLVersion: number,
): boolean {
  const state = socketState(value);
  return (
    state === null ||
    state.role === "viewer" ||
    state.aclVersion < currentACLVersion
  );
}

export function socketIsSuperseded(
  value: unknown,
  currentACLVersion: number,
): boolean {
  const state = socketState(value);
  return state === null || state.aclVersion < currentACLVersion;
}

export function socketBelongsToUser(
  value: unknown,
  userIDs: ReadonlySet<string> | null,
): boolean {
  if (userIDs === null) return true;
  const state = socketState(value);
  return state !== null && userIDs.has(state.userID);
}

export function roomIsFull(connectionCount: number): boolean {
  return connectionCount >= MAX_CONNECTIONS_PER_ROOM;
}

export function messageIsTooLarge(byteLength: number): boolean {
  return byteLength > MAX_MESSAGE_BYTES;
}

export interface TicketIDStorage {
  get<T>(key: string): Promise<T | undefined>;
  put(key: string, value: unknown): Promise<void>;
}

export async function claimTicketID(
  storage: TicketIDStorage,
  jti: string,
  storedAt = Date.now(),
): Promise<boolean> {
  const key = `jti:${jti}`;
  if ((await storage.get<number>(key)) !== undefined) return false;
  await storage.put(key, storedAt);
  return true;
}
