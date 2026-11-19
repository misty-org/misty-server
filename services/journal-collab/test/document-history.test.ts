import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import {
  HISTORY_CADENCE_MS,
  historyBytes,
  listHistory,
  maybeRecordHistory,
  pruneHistory,
  readHistoryVersion,
  recordHistoryVersion,
  thinHistory,
  type HistoryEntry,
} from "../src/document-history";
import { DOCUMENT_CHUNK_BYTES, writeDocumentSnapshot } from "../src/document-persistence";
import { restoreDocument } from "../src/document-restore";

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function entry(id: string, createdAt: number): HistoryEntry {
  return { id, createdAt, byteLength: 10, chunkCount: 1, checksum: id };
}

describe("history thinning", () => {
  const now = Date.UTC(2026, 10, 18, 12);

  it("keeps everything from the last day", () => {
    const entries = [entry("a", now - 30 * MINUTE), entry("b", now - 20 * MINUTE), entry("c", now - 10 * MINUTE)];
    expect(thinHistory(entries, now, null)).toEqual([]);
  });

  it("keeps the latest of each hour after a day and of each day after a week", () => {
    const hourStart = Math.floor((now - 2 * DAY) / HOUR) * HOUR;
    const dayStart = Math.floor((now - 10 * DAY) / DAY) * DAY;
    const entries = [
      entry("hour-early", hourStart + 5 * MINUTE),
      entry("hour-late", hourStart + 45 * MINUTE),
      entry("day-early", dayStart + HOUR),
      entry("day-late", dayStart + 20 * HOUR),
      entry("recent", now - HOUR),
    ];
    const dropped = thinHistory(entries, now, null).map((item) => item.id).sort();
    expect(dropped).toEqual(["day-early", "hour-early"]);
  });

  it("lets go of versions past the level's history window", () => {
    const entries = [entry("old", now - 40 * DAY), entry("kept", now - 2 * HOUR)];
    expect(thinHistory(entries, now, 30).map((item) => item.id)).toEqual(["old"]);
    expect(thinHistory(entries, now, null).map((item) => item.id)).toEqual([]);
  });
});

describe("history storage", () => {
  it("keeps a version every ten minutes of saves, reads it back, and counts its bytes", async () => {
    const storage = new MemoryStorage();
    const doc = new Y.Doc();
    doc.getText("misty:title").insert(0, "First");
    await writeDocumentSnapshot(asStorage(storage), Y.encodeStateAsUpdate(doc));
    const start = Date.UTC(2026, 10, 18, 9);

    expect(await maybeRecordHistory(asStorage(storage), start, null)).toBe(true);
    // Within the cadence nothing more is kept.
    expect(await maybeRecordHistory(asStorage(storage), start + HISTORY_CADENCE_MS - 1, null)).toBe(false);

    const versions = await listHistory(asStorage(storage));
    expect(versions).toHaveLength(1);
    const version = await readHistoryVersion(asStorage(storage), versions[0]!.id);
    const restored = new Y.Doc();
    Y.applyUpdate(restored, version!.update);
    expect(restored.getText("misty:title").toString()).toBe("First");
    expect(await historyBytes(asStorage(storage))).toBe(versions[0]!.byteLength);

    // The same state isn't kept twice.
    expect(await recordHistoryVersion(asStorage(storage), version!.update, start + DAY)).toBeNull();
  });

  it("chunks large versions and deletes every chunk of a version it drops", async () => {
    const storage = new MemoryStorage();
    const now = Date.UTC(2026, 10, 18, 12);
    const big = Uint8Array.from({ length: DOCUMENT_CHUNK_BYTES * 2 + 3 }, (_, index) => index % 251);
    const kept = await recordHistoryVersion(asStorage(storage), big, now - 40 * DAY);
    expect(kept?.chunkCount).toBe(3);
    expect((await readHistoryVersion(asStorage(storage), kept!.id))?.update).toEqual(big);

    await pruneHistory(asStorage(storage), now, 30);
    expect(await listHistory(asStorage(storage))).toEqual([]);
    expect(Array.from(storage.data.keys()).filter((key) => key.startsWith("history:c:"))).toEqual([]);
    expect(await historyBytes(asStorage(storage))).toBe(0);
  });
});

describe("history restore", () => {
  it("rewrites every top-level type to the version, as one new change", () => {
    const doc = new Y.Doc();
    doc.getMap<unknown>("deck:meta").set("title", "Old title");
    doc.getArray<string>("deck:order").push(["s1"]);
    const notes = new Y.Text();
    notes.insert(0, "Old notes");
    const slide = new Y.Map<unknown>();
    slide.set("notes", notes);
    doc.getMap<unknown>("deck:slides").set("s1", slide);
    const version = Y.encodeStateAsUpdate(doc);

    doc.getMap<unknown>("deck:meta").set("title", "New title");
    doc.getArray<string>("deck:order").push(["s2"]);
    doc.getMap<unknown>("deck:slides").delete("s1");

    const peer = new Y.Doc();
    Y.applyUpdate(peer, Y.encodeStateAsUpdate(doc));
    const before = Y.encodeStateVector(doc);
    restoreDocument(doc, version, "deck");
    Y.applyUpdate(peer, Y.encodeStateAsUpdate(doc, before));

    for (const copy of [doc, peer]) {
      expect(copy.getMap<unknown>("deck:meta").get("title")).toBe("Old title");
      expect(copy.getArray<string>("deck:order").toArray()).toEqual(["s1"]);
      const restoredSlide = copy.getMap<unknown>("deck:slides").get("s1") as Y.Map<unknown>;
      expect((restoredSlide.get("notes") as Y.Text).toString()).toBe("Old notes");
    }
  });

  it("restores a drawing's elements above every version an editor holds", () => {
    const doc = new Y.Doc();
    const elements = doc.getMap<Record<string, unknown>>("drawing:elements");
    elements.set("a", { id: "a", type: "text", text: "Before", version: 3 });
    const version = Y.encodeStateAsUpdate(doc);
    elements.set("a", { id: "a", type: "text", text: "After", version: 7 });
    elements.set("b", { id: "b", type: "rectangle", version: 2 });

    restoreDocument(doc, version, "drawing");
    const a = elements.get("a")!;
    const b = elements.get("b")!;
    expect(a.text).toBe("Before");
    expect(Number(a.version)).toBeGreaterThan(7);
    expect(b.isDeleted).toBe(true);
    expect(Number(b.version)).toBeGreaterThan(2);
  });

  it("keeps a note's markdown bookkeeping as it is now", () => {
    const doc = new Y.Doc();
    const metadata = doc.getMap<unknown>("misty:document");
    metadata.set("applied_version", 1);
    metadata.set("pending_version", 1);
    doc.getText("misty:markdown").insert(0, "old body");
    const fragment = doc.getXmlFragment("default");
    const paragraph = new Y.XmlElement("paragraph");
    const run = new Y.XmlText();
    run.insert(0, "old body");
    paragraph.insert(0, [run]);
    fragment.insert(0, [paragraph]);
    const version = Y.encodeStateAsUpdate(doc);

    metadata.set("applied_version", 4);
    metadata.set("pending_version", 4);
    doc.getText("misty:markdown").insert(0, "new ");
    fragment.delete(0, fragment.length);

    const room = new Y.Doc();
    Y.applyUpdate(room, Y.encodeStateAsUpdate(doc));
    restoreDocument(room, version, "note");
    expect(room.getText("misty:markdown").toString()).toBe("old body");
    expect(room.getMap<unknown>("misty:document").get("applied_version")).toBe(4);
    expect(room.getXmlFragment("default").toString()).toContain("old body");
  });
});

class MemoryStorage {
  readonly data = new Map<string, unknown>();

  async get(keyOrKeys: string | string[]): Promise<unknown> {
    if (Array.isArray(keyOrKeys)) {
      return new Map(keyOrKeys.flatMap((key) => (this.data.has(key) ? [[key, this.data.get(key)]] : [])));
    }
    return this.data.get(keyOrKeys);
  }

  async put(keyOrEntries: string | Record<string, unknown>, value?: unknown): Promise<void> {
    if (typeof keyOrEntries === "string") {
      this.data.set(keyOrEntries, value);
      return;
    }
    for (const [key, entry] of Object.entries(keyOrEntries)) this.data.set(key, entry);
  }

  async delete(keyOrKeys: string | string[]): Promise<boolean | number> {
    if (!Array.isArray(keyOrKeys)) return this.data.delete(keyOrKeys);
    let count = 0;
    for (const key of keyOrKeys) if (this.data.delete(key)) count += 1;
    return count;
  }

  async list(options: { prefix?: string } = {}): Promise<Map<string, unknown>> {
    const keys = Array.from(this.data.keys())
      .filter((key) => key.startsWith(options.prefix ?? ""))
      .sort();
    return new Map(keys.map((key) => [key, this.data.get(key)]));
  }
}

function asStorage(storage: MemoryStorage): DurableObjectStorage {
  return storage as unknown as DurableObjectStorage;
}
