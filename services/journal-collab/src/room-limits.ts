import { isRecord } from "./control-protocol";

/**
 * The limits of the Space's level that a room keeps to, as the API last answered one of its
 * callbacks. Null is unlimited, which is also what a room knows before its first callback.
 */
export interface RoomLimits {
  /** Days the room keeps earlier versions. */
  historyDays: number | null;
}

const LIMITS_KEY = "room:limits";

const UNLIMITED: RoomLimits = { historyDays: null };

function positiveLimit(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 ? value : null;
}

/** Reads the limits out of a callback's answer: `{ applied, limits: { history_days } }`. */
export function parseRoomLimits(answer: unknown): RoomLimits | null {
  if (!isRecord(answer) || !isRecord(answer.limits)) return null;
  return {
    historyDays: positiveLimit(answer.limits.history_days),
  };
}

/** Keeps the limits a callback's answer carried, when it carried any. */
export async function storeRoomLimits(storage: DurableObjectStorage, answer: unknown): Promise<void> {
  const limits = parseRoomLimits(answer);
  if (limits) await storage.put(LIMITS_KEY, limits);
}

export async function readRoomLimits(storage: DurableObjectStorage): Promise<RoomLimits> {
  const stored = await storage.get<RoomLimits>(LIMITS_KEY);
  if (!isRecord(stored)) return UNLIMITED;
  return {
    historyDays: positiveLimit(stored.historyDays),
  };
}
