import type { FileEntry, FileStore, FileStoreChange } from "./sdk.js";
import type { Person } from "./global_setup.js";
import { driveChanges, sha256, type DriveItem, type World } from "./world.js";

export interface DriveStore extends FileStore {
  known(text: string): void;
  push(item: DriveItem): void;
  writes: { text: string; base: number }[];
  close(): void;
}

const EMPTY = sha256("");

export const entryOf = (item: DriveItem): FileEntry => ({
  id: item.id,
  path: `/${item.name}`,
  name: item.name,
  kind: item.kind === "folder" ? "folder" : "file",
  size: item.size,
  mtime_ms: item.mtime_ms,
  state: "downloaded",
  version: item.version,
  content_version: item.content_version,
  ...(item.sha256 ? { sha: item.sha256 } : {}),
});

export function driveStore(drive: World, person: Person, shared?: Map<string, Uint8Array>): DriveStore {
  const contents = shared ?? new Map<string, Uint8Array>();
  contents.set(EMPTY, new Uint8Array());
  const listeners = new Set<(change: FileStoreChange) => void>();
  const writes: { text: string; base: number }[] = [];
  const known = (text: string) => contents.set(sha256(text), new TextEncoder().encode(text));
  const push = (item: DriveItem) => {
    const change: FileStoreChange = { items: [entryOf(item)] };
    for (const listener of [...listeners]) listener(change);
  };
  const stopping = driveChanges(drive.space, (itemId) => {
    void drive.item(itemId).then(push, () => undefined);
  });
  return {
    writes,
    known,
    push,
    close() {
      void stopping.then((stop) => stop());
      listeners.clear();
    },
    async entry(fileId) {
      try {
        return entryOf(await drive.item(fileId));
      } catch {
        return null;
      }
    },
    async read(entry) {
      const item = await drive.item(entry.id);
      const bytes = contents.get(item.sha256 ?? EMPTY);
      if (!bytes) throw new Error(`the test drive doesn't hold the bytes of ${item.name} (${item.sha256})`);
      return bytes.slice().buffer;
    },
    async write(entry, data, base) {
      const text = new TextDecoder().decode(data);
      writes.push({ text, base });
      known(text);
      return entryOf(await drive.write(entry.id, text, person));
    },
    async copy(entry, name, data) {
      const text = new TextDecoder().decode(data);
      known(text);
      const made = await drive.file(name, text, person);
      return entryOf(made);
    },
    watch(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    async versions(fileId) {
      const item = await drive.item(fileId);
      return { version: item.version, content_version: item.content_version };
    },
  };
}
