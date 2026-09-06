import * as encoding from "lib0/encoding";
import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import {
  DOCUMENT_CHUNK_BYTES,
  MAX_DOCUMENT_BYTES,
  projectedDocumentBytes,
  readDocumentSnapshot,
  writeDocumentSnapshot,
  type SnapshotManifest,
} from "../src/document-persistence";

describe("document snapshot persistence", () => {
  it("round-trips a multi-chunk generation with checksum validation", async () => {
    const storage = new MemoryStorage();
    const update = patternedBytes(DOCUMENT_CHUNK_BYTES * 2 + 17, 11);

    const manifest = await writeDocumentSnapshot(asStorage(storage), update);
    const restored = await readDocumentSnapshot(asStorage(storage));

    expect(manifest.chunkCount).toBe(3);
    expect(restored?.source).toBe("current");
    expect(restored?.update).toEqual(update);
  });

  it("keeps the prior committed generation when manifest commit is interrupted", async () => {
    const storage = new MemoryStorage();
    const original = patternedBytes(DOCUMENT_CHUNK_BYTES + 5, 3);
    await writeDocumentSnapshot(asStorage(storage), original);

    storage.failNextManifestCommit = true;
    await expect(
      writeDocumentSnapshot(asStorage(storage), patternedBytes(DOCUMENT_CHUNK_BYTES + 8, 7)),
    ).rejects.toThrow("simulated manifest failure");

    const restored = await readDocumentSnapshot(asStorage(storage));
    expect(restored?.source).toBe("current");
    expect(restored?.update).toEqual(original);
  });

  it("falls back to the retained previous generation when current data is corrupt", async () => {
    const storage = new MemoryStorage();
    const previous = patternedBytes(DOCUMENT_CHUNK_BYTES + 9, 13);
    await writeDocumentSnapshot(asStorage(storage), previous);
    await writeDocumentSnapshot(asStorage(storage), patternedBytes(DOCUMENT_CHUNK_BYTES + 12, 17));

    const current = storage.value<SnapshotManifest>("doc:v2:current");
    storage.set(`doc:v2:${current.generation}:0`, patternedBytes(DOCUMENT_CHUNK_BYTES, 99).buffer);

    const restored = await readDocumentSnapshot(asStorage(storage));
    expect(restored?.source).toBe("previous");
    expect(restored?.update).toEqual(previous);
  });

  it("loads legacy chunks so rooms migrate without data loss", async () => {
    const storage = new MemoryStorage();
    const update = patternedBytes(DOCUMENT_CHUNK_BYTES + 4, 23);
    storage.set("doc:chunks", 2);
    storage.set("doc:0", update.slice(0, DOCUMENT_CHUNK_BYTES).buffer);
    storage.set("doc:1", update.slice(DOCUMENT_CHUNK_BYTES).buffer);

    const restored = await readDocumentSnapshot(asStorage(storage));
    expect(restored?.source).toBe("legacy");
    expect(restored?.update).toEqual(update);
  });

  it("rejects a snapshot above the persisted document ceiling", async () => {
    const storage = new MemoryStorage();
    await expect(
      writeDocumentSnapshot(asStorage(storage), new Uint8Array(MAX_DOCUMENT_BYTES + 1)),
    ).rejects.toThrow("document_limit_exceeded");
  });
});

describe("incoming document ceiling projection", () => {
  it("measures a Yjs sync update without mutating the live document", () => {
    const live = new Y.Doc();
    live.getText("content").insert(0, "saved");
    const incoming = new Y.Doc();
    Y.applyUpdate(incoming, Y.encodeStateAsUpdate(live));
    incoming.getText("content").insert(5, " plus incoming");

    const message = syncUpdateMessage(Y.encodeStateAsUpdate(incoming));
    const projected = projectedDocumentBytes(live, message);

    expect(projected).toBe(Y.encodeStateAsUpdate(incoming).byteLength);
    expect(live.getText("content").toString()).toBe("saved");
  });

  it("ignores awareness and sync-step-one messages", () => {
    const document = new Y.Doc();
    const awareness = encoding.createEncoder();
    encoding.writeVarUint(awareness, 1);
    const syncStepOne = encoding.createEncoder();
    encoding.writeVarUint(syncStepOne, 0);
    encoding.writeVarUint(syncStepOne, 0);

    expect(projectedDocumentBytes(document, encoding.toUint8Array(awareness))).toBeNull();
    expect(projectedDocumentBytes(document, encoding.toUint8Array(syncStepOne))).toBeNull();
  });
});

class MemoryStorage {
  readonly data = new Map<string, unknown>();
  failNextManifestCommit = false;

  async get(keyOrKeys: string | string[]): Promise<unknown> {
    if (Array.isArray(keyOrKeys)) {
      return new Map(keyOrKeys.flatMap((key) => (this.data.has(key) ? [[key, this.data.get(key)]] : [])));
    }
    return this.data.get(keyOrKeys);
  }

  async put(keyOrEntries: string | Record<string, unknown>, value?: unknown): Promise<void> {
    if (typeof keyOrEntries === "string") {
      if (keyOrEntries === "doc:v2:current" && this.consumeManifestFailure()) {
        throw new Error("simulated manifest failure");
      }
      this.data.set(keyOrEntries, value);
      return;
    }
    if (Object.hasOwn(keyOrEntries, "doc:v2:current") && this.consumeManifestFailure()) {
      throw new Error("simulated manifest failure");
    }
    for (const [key, entry] of Object.entries(keyOrEntries)) this.data.set(key, entry);
  }

  async delete(keyOrKeys: string | string[]): Promise<boolean | number> {
    if (!Array.isArray(keyOrKeys)) return this.data.delete(keyOrKeys);
    let count = 0;
    for (const key of keyOrKeys) if (this.data.delete(key)) count += 1;
    return count;
  }

  set(key: string, value: unknown): void {
    this.data.set(key, value);
  }

  value<T>(key: string): T {
    const value = this.data.get(key);
    if (value === undefined) throw new Error(`missing test value ${key}`);
    return value as T;
  }

  private consumeManifestFailure(): boolean {
    if (!this.failNextManifestCommit) return false;
    this.failNextManifestCommit = false;
    return true;
  }
}

function asStorage(storage: MemoryStorage): DurableObjectStorage {
  return storage as unknown as DurableObjectStorage;
}

function patternedBytes(length: number, seed: number): Uint8Array {
  return Uint8Array.from({ length }, (_, index) => (index * 31 + seed) % 256);
}

function syncUpdateMessage(update: Uint8Array): Uint8Array {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, 0);
  encoding.writeVarUint(encoder, 2);
  encoding.writeVarUint8Array(encoder, update);
  return encoding.toUint8Array(encoder);
}
