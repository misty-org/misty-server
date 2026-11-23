import { isRecord } from "./control_protocol";

export interface RoomLimits {
  historyDays: number | null;
}

const LIMITS_KEY = "room:limits";

const UNLIMITED: RoomLimits = { historyDays: null };

function positiveLimit(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 ? value : null;
}

export function parseRoomLimits(answer: unknown): RoomLimits | null {
  if (!isRecord(answer) || !isRecord(answer.limits)) return null;
  return {
    historyDays: positiveLimit(answer.limits.history_days),
  };
}

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
