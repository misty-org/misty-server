import { constants } from "node:fs";
import { mkdir, open, realpath, lstat, unlink, statfs } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
import { Readable } from "node:stream";
import { objectKey, objectMetadata, StorageUnavailable, type ByteObjectStore, type ObjectStore, type StreamingObjectStore } from "./object-store.js";
import { verifiedObjectStream } from "./verified-stream.js";

const maxTransferBytes = 16 * 1024 * 1024;
const missing = (error: unknown) => error !== null && typeof error === "object" && "code" in error && error.code === "ENOENT";
const digest = (data: Buffer) => createHash("sha256").update(data).digest("hex");

/** Go-compatible private local objects. Writes are exclusively to fresh keys;
 * the caller publishes the database reference only after this method succeeds.
 * The configured directory and its ancestors must be controlled by the operator.
 */
export async function createFilesystemByteStore(directory: string): Promise<ByteObjectStore & StreamingObjectStore & Pick<ObjectStore, "delete">> {
  if (!directory.trim() || directory.includes("\0")) throw new StorageUnavailable();
  let root: string;
  try {
    await mkdir(resolve(directory), { recursive: true, mode: 0o700 });
    root = await realpath(resolve(directory));
    await mkdir(join(root, "avatars"), { mode: 0o700 }).catch((error: unknown) => {
      if (!error || typeof error !== "object" || !("code" in error) || error.code !== "EEXIST") throw error;
    });
    await checkDirectory(root); await checkDirectory(join(root, "avatars"));
    await syncDirectory(root);
  } catch { throw new StorageUnavailable(); }
  let active = 0;
  async function bounded<T>(signal: AbortSignal | undefined, run: (abort: AbortSignal) => Promise<T>) {
    if (active >= 16) throw new StorageUnavailable();
    active++;
    try {
      const abort = AbortSignal.any([AbortSignal.timeout(10000), ...(signal ? [signal] : [])]);
      abort.throwIfAborted();
      return await run(abort);
    } catch { throw new StorageUnavailable(); }
    finally { active--; }
  }
  async function paths(key: string) {
    objectKey(key);
    // The legacy format strips library/, but retains avatars/.
    const name = key.startsWith("library/") ? key.slice("library/".length) : key;
    const parent = key.startsWith("avatars/") ? join(root, "avatars") : root;
    await checkDirectory(root); await checkDirectory(parent);
    return { blob: join(root, `${name}.blob`), metadata: join(root, `${name}.json`), parent };
  }
  return {
    async openStream(key, signal) {
      if (active >= 16) throw new StorageUnavailable();
      active++;
      let handle: Awaited<ReturnType<typeof open>> | undefined;
      try {
        const abort = AbortSignal.any([AbortSignal.timeout(300000), ...(signal ? [signal] : [])]);
        abort.throwIfAborted();
        const path = await paths(key), raw = JSON.parse((await readBounded(path.metadata, 4096, abort)).toString("utf8"));
        const metadata = objectMetadata({ byteSize: raw?.ByteSize, sha256: raw?.SHA256, mimeType: raw?.MIMEType });
        handle = await open(path.blob, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
        const info = await handle.stat();
        if (!info.isFile() || info.size !== metadata.byteSize) throw new StorageUnavailable();
        abort.throwIfAborted();
        const body = Readable.toWeb(handle.createReadStream({ autoClose: true, highWaterMark: 64 * 1024 }),
          { strategy: { highWaterMark: 64 * 1024, size: chunk => chunk.byteLength } }) as ReadableStream<Uint8Array>;
        return { metadata, body: verifiedObjectStream(body, metadata, abort, () => { active--; }) };
      } catch (error) {
        await handle?.close().catch(() => {}); active--;
        if (missing(error)) return null;
        throw new StorageUnavailable();
      }
    },
    async getBytes(key, maxBytes, signal) {
      return bounded(signal, async (abort) => {
        if (!Number.isSafeInteger(maxBytes) || maxBytes < 1 || maxBytes > maxTransferBytes) throw new StorageUnavailable();
        const path = await paths(key);
        try {
          const raw: unknown = JSON.parse((await readBounded(path.metadata, 4096, abort)).toString("utf8"));
          if (!raw || typeof raw !== "object") throw new StorageUnavailable();
          const metadata = raw as Record<string, unknown>;
          const validated = objectMetadata({ byteSize: metadata.ByteSize as number, sha256: metadata.SHA256 as string, mimeType: metadata.MIMEType as string });
          if (validated.byteSize > maxBytes) throw new StorageUnavailable();
          const data = await readBounded(path.blob, maxBytes, abort);
          if (data.length !== validated.byteSize || digest(data) !== validated.sha256) throw new StorageUnavailable();
          return data;
        } catch (error) { if (missing(error)) return null; throw error; }
      });
    },
    async putBytes(key, data, metadata, signal) {
      await bounded(signal, async (abort) => {
        objectMetadata(metadata);
        if (data.length > maxTransferBytes || data.length !== metadata.byteSize || digest(data) !== metadata.sha256) throw new StorageUnavailable();
        const path = await paths(key), capacity = await statfs(root, { bigint: true });
        if (capacity.bavail * capacity.bsize - BigInt(data.length) < 64n * 1024n * 1024n) throw new StorageUnavailable();
        const created: string[] = [];
        try {
          // No temporary files: a crash leaves only the key already registered
          // in the durable deletion queue. Exclusive create cannot overwrite a
          // previously published object or follow an existing symbolic link.
          for (const [file, bytes] of [[path.blob, data], [path.metadata, Buffer.from(JSON.stringify({
            ByteSize: metadata.byteSize, SHA256: metadata.sha256, MIMEType: metadata.mimeType,
          }))]] as const) {
            abort.throwIfAborted();
            const handle = await open(file, "wx", 0o600); created.push(file);
            try { await handle.writeFile(bytes, { signal: abort }); await handle.sync(); }
            finally { await handle.close(); }
          }
          await syncDirectory(path.parent); abort.throwIfAborted();
        } catch (error) {
          for (const file of created.reverse()) await unlink(file).catch(() => {});
          throw error;
        }
      });
    },
    async delete(key, signal) {
      await bounded(signal, async (abort) => {
        const path = await paths(key);
        for (const file of [path.metadata, path.blob]) {
          abort.throwIfAborted();
          await unlink(file).catch((error: unknown) => { if (!missing(error)) throw error; });
        }
        await syncDirectory(path.parent);
      });
    },
  };
}

async function checkDirectory(path: string) {
  const info = await lstat(path);
  if (!info.isDirectory() || info.isSymbolicLink() || await realpath(path) !== path) throw new StorageUnavailable();
}
async function syncDirectory(path: string) {
  const handle = await open(path, constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW);
  try { await handle.sync(); } finally { await handle.close(); }
}
async function readBounded(path: string, limit: number, signal: AbortSignal) {
  // NONBLOCK avoids waiting forever on a substituted FIFO before fstat.
  const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const info = await handle.stat();
    if (!info.isFile() || info.size > limit) throw new StorageUnavailable();
    const data = Buffer.alloc(info.size + 1); let offset = 0;
    while (offset < data.length) {
      signal.throwIfAborted();
      const { bytesRead } = await handle.read(data, offset, data.length - offset, offset);
      if (!bytesRead) break;
      offset += bytesRead;
    }
    signal.throwIfAborted();
    if (offset !== info.size) throw new StorageUnavailable();
    return data.subarray(0, offset);
  } finally { await handle.close(); }
}
