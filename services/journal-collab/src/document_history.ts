import {
  DOCUMENT_CHUNK_BYTES,
  MAX_DOCUMENT_BYTES,
  currentSnapshotManifest,
  readDocumentSnapshot,
  sha256,
} from "./document_persistence";

export const HISTORY_CADENCE_MS = 10 * 60 * 1000;
const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

const ENTRY_PREFIX = "history:v:";
const CHUNK_PREFIX = "history:c:";
const LAST_KEY = "history:last";
const BYTES_KEY = "history:bytes";
const KEYS_PER_CALL = 128;

export interface HistoryEntry {
  id: string;
  createdAt: number;
  byteLength: number;
  chunkCount: number;
  checksum: string;
}

export interface HistoryVersion {
  entry: HistoryEntry;
  update: Uint8Array;
}

function isHistoryEntry(value: unknown): value is HistoryEntry {
  if (!value || typeof value !== "object") return false;
  const entry = value as Partial<HistoryEntry>;
  return (
    typeof entry.id === "string" &&
    /^[A-Za-z0-9_-]{1,64}$/u.test(entry.id) &&
    Number.isFinite(entry.createdAt) &&
    Number.isInteger(entry.byteLength) &&
    Number(entry.byteLength) >= 0 &&
    Number(entry.byteLength) <= MAX_DOCUMENT_BYTES &&
    Number.isInteger(entry.chunkCount) &&
    Number(entry.chunkCount) >= 0 &&
    Number(entry.chunkCount) <= 128 &&
    typeof entry.checksum === "string"
  );
}

function entryKey(entry: HistoryEntry): string {
  return `${ENTRY_PREFIX}${String(Math.max(0, Math.floor(entry.createdAt))).padStart(15, "0")}:${entry.id}`;
}

function chunkKeys(entry: HistoryEntry): string[] {
  return Array.from({ length: entry.chunkCount }, (_, index) => `${CHUNK_PREFIX}${entry.id}:${index}`);
}

async function deleteKeys(storage: DurableObjectStorage, keys: string[]): Promise<void> {
  for (let offset = 0; offset < keys.length; offset += KEYS_PER_CALL) {
    await storage.delete(keys.slice(offset, offset + KEYS_PER_CALL));
  }
}

export async function listHistory(storage: DurableObjectStorage): Promise<HistoryEntry[]> {
  const stored = await storage.list<HistoryEntry>({ prefix: ENTRY_PREFIX });
  return Array.from(stored.values()).filter(isHistoryEntry).sort((left, right) => left.createdAt - right.createdAt);
}

export async function historyBytes(storage: DurableObjectStorage): Promise<number> {
  const bytes = await storage.get<number>(BYTES_KEY);
  return typeof bytes === "number" && Number.isFinite(bytes) && bytes >= 0 ? bytes : 0;
}

export async function recordHistoryVersion(
  storage: DurableObjectStorage,
  update: Uint8Array,
  createdAt: number,
  checksum?: string,
): Promise<HistoryEntry | null> {
  if (update.byteLength > MAX_DOCUMENT_BYTES) return null;
  const digest = checksum ?? (await sha256(update));
  const existing = await listHistory(storage);
  const newest = existing[existing.length - 1];
  if (newest && newest.checksum === digest) return null;

  const entry: HistoryEntry = {
    id: `v_${crypto.randomUUID().replace(/-/gu, "")}`,
    createdAt,
    byteLength: update.byteLength,
    chunkCount: 0,
    checksum: digest,
  };
  const chunks: Record<string, ArrayBuffer> = {};
  for (let offset = 0; offset < update.byteLength; offset += DOCUMENT_CHUNK_BYTES) {
    chunks[`${CHUNK_PREFIX}${entry.id}:${entry.chunkCount}`] = new Uint8Array(
      update.slice(offset, offset + DOCUMENT_CHUNK_BYTES),
    ).buffer;
    entry.chunkCount += 1;
  }
  if (entry.chunkCount > 0) await storage.put(chunks);
  await storage.put(entryKey(entry), entry);
  await storage.put(BYTES_KEY, (await historyBytes(storage)) + entry.byteLength);
  return entry;
}

export function thinHistory(
  entries: readonly HistoryEntry[],
  now: number,
  retentionDays: number | null,
): HistoryEntry[] {
  const latestInBucket = new Map<string, HistoryEntry>();
  const drop: HistoryEntry[] = [];
  const sorted = [...entries].sort((left, right) => left.createdAt - right.createdAt);
  for (const entry of sorted) {
    const age = now - entry.createdAt;
    if (retentionDays !== null && age > retentionDays * DAY_MS) {
      drop.push(entry);
      continue;
    }
    let bucket: string | null = null;
    if (age > 7 * DAY_MS) bucket = `day:${Math.floor(entry.createdAt / DAY_MS)}`;
    else if (age > DAY_MS) bucket = `hour:${Math.floor(entry.createdAt / HOUR_MS)}`;
    if (bucket === null) continue;
    const kept = latestInBucket.get(bucket);
    if (kept) drop.push(kept);
    latestInBucket.set(bucket, entry);
  }
  return drop;
}

export async function pruneHistory(
  storage: DurableObjectStorage,
  now: number,
  retentionDays: number | null,
): Promise<void> {
  const entries = await listHistory(storage);
  const drop = thinHistory(entries, now, retentionDays);
  if (drop.length > 0) {
    await deleteKeys(storage, drop.map(entryKey));
    await deleteKeys(storage, drop.flatMap(chunkKeys));
  }
  const dropped = new Set(drop.map((entry) => entry.id));
  const kept = entries.filter((entry) => !dropped.has(entry.id));
  await storage.put(
    BYTES_KEY,
    kept.reduce((total, entry) => total + entry.byteLength, 0),
  );
}

export async function readHistoryVersion(
  storage: DurableObjectStorage,
  versionID: string,
): Promise<HistoryVersion | null> {
  const entry = (await listHistory(storage)).find((candidate) => candidate.id === versionID);
  if (!entry) return null;
  const keys = chunkKeys(entry);
  const parts: Uint8Array[] = [];
  for (let offset = 0; offset < keys.length; offset += KEYS_PER_CALL) {
    const stored = await storage.get<ArrayBuffer>(keys.slice(offset, offset + KEYS_PER_CALL));
    for (const key of keys.slice(offset, offset + KEYS_PER_CALL)) {
      const chunk = stored.get(key);
      if (!(chunk instanceof ArrayBuffer)) return null;
      parts.push(new Uint8Array(chunk));
    }
  }
  const update = new Uint8Array(entry.byteLength);
  let written = 0;
  for (const part of parts) {
    if (written + part.byteLength > update.byteLength) return null;
    update.set(part, written);
    written += part.byteLength;
  }
  if (written !== entry.byteLength || (await sha256(update)) !== entry.checksum) return null;
  return { entry, update };
}

export async function maybeRecordHistory(
  storage: DurableObjectStorage,
  now: number,
  retentionDays: number | null,
): Promise<boolean> {
  const last = (await storage.get<number>(LAST_KEY)) ?? 0;
  if (now - last < HISTORY_CADENCE_MS) return false;
  const manifest = await currentSnapshotManifest(storage);
  const previous = await readDocumentSnapshot(storage);
  if (!previous) return false;
  const checksum = previous.source === "current" ? manifest?.checksum : undefined;
  const createdAt = previous.source === "current" && manifest ? manifest.savedAt : now;
  await recordHistoryVersion(storage, previous.update, createdAt, checksum);
  await storage.put(LAST_KEY, now);
  await pruneHistory(storage, now, retentionDays);
  return true;
}

export async function recordHistoryNow(
  storage: DurableObjectStorage,
  update: Uint8Array,
  now: number,
  retentionDays: number | null,
): Promise<void> {
  await recordHistoryVersion(storage, update, now);
  await storage.put(LAST_KEY, now);
  await pruneHistory(storage, now, retentionDays);
}
