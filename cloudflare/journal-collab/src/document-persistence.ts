import * as decoding from "lib0/decoding";
import * as Y from "yjs";

/** Durable Object values are capped at 128 KiB; leave room for metadata. */
export const DOCUMENT_CHUNK_BYTES = 96 * 1024;
/** Public-beta ceiling for the compact Yjs state of one note or drawing. */
export const MAX_DOCUMENT_BYTES = 8 * 1024 * 1024;
export const DOCUMENT_WARNING_BYTES = Math.floor(MAX_DOCUMENT_BYTES * 0.8);

const CURRENT_MANIFEST_KEY = "doc:v2:current";
const PREVIOUS_MANIFEST_KEY = "doc:v2:previous";
const LEGACY_CHUNK_COUNT_KEY = "doc:chunks";
const SNAPSHOT_VERSION = 2;
const MESSAGE_SYNC = 0;
const SYNC_STEP_2 = 1;
const SYNC_UPDATE = 2;

export interface SnapshotManifest {
  version: 2;
  generation: string;
  chunkCount: number;
  byteLength: number;
  checksum: string;
  savedAt: number;
}

export interface SnapshotReadResult {
  update: Uint8Array;
  source: "current" | "previous" | "legacy";
}

/**
 * Writes a generation under unique keys and commits its manifest last.
 *
 * A crash before the manifest write leaves the previous generation selected.
 * One previous generation is retained so a corrupt/missing current chunk can
 * be recovered without accepting a partially written Yjs update.
 */
export async function writeDocumentSnapshot(
  storage: DurableObjectStorage,
  update: Uint8Array,
): Promise<SnapshotManifest> {
  if (update.byteLength > MAX_DOCUMENT_BYTES) {
    throw new Error("document_limit_exceeded");
  }

  const current = await storage.get<SnapshotManifest>(CURRENT_MANIFEST_KEY);
  const previous = await storage.get<SnapshotManifest>(PREVIOUS_MANIFEST_KEY);
  const generation = crypto.randomUUID();
  const chunks: Record<string, ArrayBuffer> = {};
  let chunkCount = 0;

  for (let offset = 0; offset < update.byteLength; offset += DOCUMENT_CHUNK_BYTES) {
    const slice = update.slice(offset, offset + DOCUMENT_CHUNK_BYTES);
    chunks[chunkKey(generation, chunkCount)] = new Uint8Array(slice).buffer;
    chunkCount += 1;
  }

  const manifest: SnapshotManifest = {
    version: SNAPSHOT_VERSION,
    generation,
    chunkCount,
    byteLength: update.byteLength,
    checksum: await sha256(update),
    savedAt: Date.now(),
  };

  await storage.put(chunks);
  if (isSnapshotManifest(current)) {
    await storage.put({
      [CURRENT_MANIFEST_KEY]: manifest,
      [PREVIOUS_MANIFEST_KEY]: current,
    });
  } else {
    await storage.put(CURRENT_MANIFEST_KEY, manifest);
  }

  if (isSnapshotManifest(previous) && previous.generation !== current?.generation) {
    await storage.delete(manifestChunkKeys(previous));
  }
  return manifest;
}

/** Reads the newest intact generation, falling back to the previous or v1 data. */
export async function readDocumentSnapshot(
  storage: DurableObjectStorage,
): Promise<SnapshotReadResult | null> {
  const current = await storage.get<SnapshotManifest>(CURRENT_MANIFEST_KEY);
  const currentUpdate = await readManifest(storage, current);
  if (currentUpdate) return { update: currentUpdate, source: "current" };

  const previous = await storage.get<SnapshotManifest>(PREVIOUS_MANIFEST_KEY);
  const previousUpdate = await readManifest(storage, previous);
  if (previousUpdate) return { update: previousUpdate, source: "previous" };

  const legacy = await readLegacySnapshot(storage);
  return legacy ? { update: legacy, source: "legacy" } : null;
}

/**
 * Returns the compact document size after applying an incoming Yjs update.
 * Non-update protocol messages return null.
 */
export function projectedDocumentBytes(document: Y.Doc, message: Uint8Array): number | null {
  try {
    const decoder = decoding.createDecoder(message);
    if (decoding.readVarUint(decoder) !== MESSAGE_SYNC) return null;
    const syncType = decoding.readVarUint(decoder);
    if (syncType !== SYNC_STEP_2 && syncType !== SYNC_UPDATE) return null;
    const incomingUpdate = decoding.readVarUint8Array(decoder);

    const projected = new Y.Doc();
    try {
      Y.applyUpdate(projected, Y.encodeStateAsUpdate(document));
      Y.applyUpdate(projected, incomingUpdate);
      return Y.encodeStateAsUpdate(projected).byteLength;
    } finally {
      projected.destroy();
    }
  } catch {
    // Let y-partyserver handle malformed protocol messages consistently.
    return null;
  }
}

function manifestChunkKeys(manifest: SnapshotManifest): string[] {
  return Array.from({ length: manifest.chunkCount }, (_, index) =>
    chunkKey(manifest.generation, index),
  );
}

async function readManifest(
  storage: DurableObjectStorage,
  value: SnapshotManifest | undefined,
): Promise<Uint8Array | null> {
  if (!isSnapshotManifest(value)) return null;
  const keys = manifestChunkKeys(value);
  const stored = await storage.get<ArrayBuffer>(keys);
  const parts: Uint8Array[] = [];
  let total = 0;

  for (const key of keys) {
    const chunk = stored.get(key);
    if (!(chunk instanceof ArrayBuffer)) return null;
    const part = new Uint8Array(chunk);
    parts.push(part);
    total += part.byteLength;
  }
  if (total !== value.byteLength || total > MAX_DOCUMENT_BYTES) return null;

  const update = concatenate(parts, total);
  return (await sha256(update)) === value.checksum ? update : null;
}

async function readLegacySnapshot(storage: DurableObjectStorage): Promise<Uint8Array | null> {
  const chunkCount = (await storage.get<number>(LEGACY_CHUNK_COUNT_KEY)) ?? 0;
  if (!Number.isInteger(chunkCount) || chunkCount < 1 || chunkCount > 128) return null;
  const keys = Array.from({ length: chunkCount }, (_, index) => `doc:${index}`);
  const stored = await storage.get<ArrayBuffer>(keys);
  const parts: Uint8Array[] = [];
  let total = 0;

  for (const key of keys) {
    const chunk = stored.get(key);
    if (!(chunk instanceof ArrayBuffer)) return null;
    const part = new Uint8Array(chunk);
    parts.push(part);
    total += part.byteLength;
  }
  if (total > MAX_DOCUMENT_BYTES) return null;
  return concatenate(parts, total);
}

function concatenate(parts: Uint8Array[], total: number): Uint8Array {
  const update = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    update.set(part, offset);
    offset += part.byteLength;
  }
  return update;
}

function chunkKey(generation: string, index: number): string {
  return `doc:v2:${generation}:${index}`;
}

function isSnapshotManifest(value: unknown): value is SnapshotManifest {
  if (!value || typeof value !== "object") return false;
  const manifest = value as Partial<SnapshotManifest>;
  return (
    manifest.version === SNAPSHOT_VERSION &&
    typeof manifest.generation === "string" &&
    manifest.generation.length > 0 &&
    Number.isInteger(manifest.chunkCount) &&
    Number(manifest.chunkCount) > 0 &&
    Number(manifest.chunkCount) <= 128 &&
    Number.isInteger(manifest.byteLength) &&
    Number(manifest.byteLength) >= 0 &&
    Number(manifest.byteLength) <= MAX_DOCUMENT_BYTES &&
    typeof manifest.checksum === "string" &&
    /^[a-f0-9]{64}$/.test(manifest.checksum) &&
    Number.isFinite(manifest.savedAt)
  );
}

async function sha256(value: Uint8Array): Promise<string> {
  const input = new Uint8Array(value).buffer;
  const digest = await crypto.subtle.digest("SHA-256", input);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
