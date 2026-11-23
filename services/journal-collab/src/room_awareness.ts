import * as decoding from "lib0/decoding";
import type { Connection } from "partyserver";

const MESSAGE_AWARENESS = 1;
const AWARENESS_IDS_KEY = "__ypsAwarenessIds";

export function awarenessClientIDs(message: ArrayBuffer): number[] | null {
  try {
    const decoder = decoding.createDecoder(new Uint8Array(message));
    if (decoding.readVarUint(decoder) !== MESSAGE_AWARENESS) return null;
    const update = decoding.createDecoder(decoding.readVarUint8Array(decoder));
    const count = decoding.readVarUint(update);
    const ids: number[] = [];
    for (let index = 0; index < count; index += 1) {
      ids.push(decoding.readVarUint(update));
      decoding.readVarUint(update);
      decoding.readVarString(update);
    }
    return ids;
  } catch {
    return null;
  }
}

export function awarenessIDsOf(connection: Connection): number[] {
  const state = connection.state as Record<string, unknown> | null;
  const ids = state?.[AWARENESS_IDS_KEY];
  return Array.isArray(ids) ? ids.filter((id): id is number => typeof id === "number") : [];
}

function writeAwarenessIDs(connection: Connection, ids: Iterable<number>): void {
  try {
    connection.setState((previous: unknown) => ({
      ...((previous ?? {}) as Record<string, unknown>),
      [AWARENESS_IDS_KEY]: [...ids],
    }));
  } catch {
  }
}

export function claimAwarenessIDs(
  owner: Connection,
  claimed: readonly number[],
  removed: readonly number[],
  others: Iterable<Connection>,
): void {
  const mine = new Set(awarenessIDsOf(owner));
  let changed = false;
  for (const id of claimed) {
    if (!mine.has(id)) changed = true;
    mine.add(id);
  }
  for (const id of removed) {
    if (mine.delete(id)) changed = true;
  }
  if (changed) writeAwarenessIDs(owner, mine);
  const moved = new Set([...claimed, ...removed]);
  if (moved.size === 0) return;
  for (const other of others) {
    if (other.id === owner.id) continue;
    const ids = awarenessIDsOf(other);
    if (ids.some((id) => moved.has(id))) {
      writeAwarenessIDs(other, ids.filter((id) => !moved.has(id)));
    }
  }
}

export function awarenessOwnedBySomeoneElse(
  sender: Connection,
  ids: readonly number[],
  others: Iterable<Connection>,
): boolean {
  if (ids.length === 0) return false;
  const senderUser = (sender.state as { userID?: unknown } | null)?.userID;
  const wanted = new Set(ids);
  for (const other of others) {
    if (other.id === sender.id) continue;
    if ((other.state as { userID?: unknown } | null)?.userID === senderUser) continue;
    if (awarenessIDsOf(other).some((id) => wanted.has(id))) return true;
  }
  return false;
}
