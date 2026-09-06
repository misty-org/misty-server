import { mkdtemp, rm, readFile, writeFile, mkdir, symlink, stat, readdir, unlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { afterEach, expect, it } from "vitest";
import { createFilesystemByteStore } from "./filesystem-store.js";
import { loadFilesystemConfig, storageBackend } from "./filesystem-config.js";

const directories: string[] = [];
const data = Buffer.from("private account image bytes");
const metadata = { byteSize: data.length, sha256: createHash("sha256").update(data).digest("hex"), mimeType: "image/png" };
const legacyMetadata = { ByteSize: metadata.byteSize, SHA256: metadata.sha256, MIMEType: metadata.mimeType };
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "misty-object-test-")); directories.push(root);
  return { root, store: await createFilesystemByteStore(root) };
}
afterEach(async () => { for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true }); });

it("streams legacy objects larger than the byte API limit and releases cancelled transfers", async () => {
  const { root, store } = await fixture(), bytes = Buffer.alloc(20 * 1024 * 1024, 42), key = "library/stream_12345678";
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  await writeFile(join(root, "stream_12345678.blob"), bytes);
  await writeFile(join(root, "stream_12345678.json"), JSON.stringify({ ByteSize: bytes.length, SHA256: sha256, MIMEType: "application/octet-stream" }));
  const opened = await store.openStream(key); expect(opened?.metadata.byteSize).toBe(bytes.length);
  const reader = opened!.body.getReader(); let total = 0;
  for (;;) { const value = await reader.read(); if (value.done) break; expect(value.value.length).toBeLessThanOrEqual(64 * 1024); total += value.value.length; }
  expect(total).toBe(bytes.length); reader.releaseLock();
  // More cancellations than the open-file budget proves slots are released.
  for (let index = 0; index < 17; index++) await (await store.openStream(key))!.body.cancel();
  const abort = new AbortController(), cancelled = await store.openStream(key, abort.signal); abort.abort();
  await expect(cancelled!.body.getReader().read()).rejects.toThrow("Object storage is unavailable");
  expect(await store.openStream("library/missing00")).toBeNull();
});

it("fails a streamed checksum mismatch and refuses symbolic links", async () => {
  const { root, store } = await fixture(), key = "library/checksum123";
  await store.putBytes(key, data, metadata);
  await writeFile(join(root, "checksum123.blob"), Buffer.alloc(data.length, 0));
  const opened = await store.openStream(key);
  // The corrupt final chunk must not satisfy a client's Content-Length first.
  await expect(opened!.body.getReader().read()).rejects.toThrow("Object storage is unavailable");
  await unlink(join(root, "checksum123.blob")); await symlink(join(root, "checksum123.json"), join(root, "checksum123.blob"));
  await expect(store.openStream(key)).rejects.toThrow("Object storage is unavailable");
});

it("persists private Go-format objects, survives reopening, and never overwrites an existing key", async () => {
  const { root, store } = await fixture();
  for (const key of ["avatars/avatar_12345678", "library/12345678"]) {
    await store.putBytes(key, data, metadata);
    const name = key.replace(/^library\//, "");
    expect(await readFile(join(root, `${name}.blob`))).toEqual(data);
    expect(JSON.parse(await readFile(join(root, `${name}.json`), "utf8"))).toEqual(legacyMetadata);
    expect((await stat(join(root, `${name}.blob`))).mode & 0o777).toBe(0o600);
    expect((await stat(join(root, `${name}.json`))).mode & 0o777).toBe(0o600);
    await expect(store.putBytes(key, data, metadata)).rejects.toThrow("Object storage is unavailable");
    expect(await (await createFilesystemByteStore(root)).getBytes(key, 100)).toEqual(data);
  }
  expect((await stat(join(root, "avatars"))).mode & 0o777).toBe(0o700);
});
it("reads legacy files and rejects truncated, enlarged, corrupted or oversized metadata without unbounded reads", async () => {
  const { root, store } = await fixture(), key = "avatars/user_12345678", file = join(root, `${key}.blob`), sidecar = join(root, `${key}.json`);
  await writeFile(file, data); await writeFile(sidecar, JSON.stringify(legacyMetadata));
  expect(await store.getBytes(key, 100)).toEqual(data);
  await expect(store.getBytes(key, data.length - 1)).rejects.toThrow("Object storage is unavailable");
  for (const changed of [data.subarray(1), Buffer.concat([data, Buffer.from("extra")]), Buffer.alloc(data.length, 0)]) {
    await writeFile(file, changed);
    await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
  }
  await writeFile(file, data);
  for (const changed of ["{", "null", JSON.stringify({ ...legacyMetadata, ByteSize: -1 }), " ".repeat(4097)]) {
    await writeFile(sidecar, changed);
    await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
  }
});
it("treats missing pairs as absent and removes crash remnants idempotently", async () => {
  const { root, store } = await fixture(), key = "avatars/avatar_12345678", file = join(root, `${key}.blob`), sidecar = join(root, `${key}.json`);
  expect(await store.getBytes(key, 100)).toBeNull();
  await writeFile(file, data);
  expect(await store.getBytes(key, 100)).toBeNull();
  await store.delete(key); await store.delete(key);
  await writeFile(sidecar, JSON.stringify(legacyMetadata));
  // Publication failure removes only the newly created blob, preserving the
  // pre-existing sidecar. The durable deletion job can remove that crash remnant.
  await expect(store.putBytes(key, data, metadata)).rejects.toThrow("Object storage is unavailable");
  expect(await readdir(join(root, "avatars"))).toEqual(["avatar_12345678.json"]);
  await store.delete(key);
  expect(await readdir(join(root, "avatars"))).toEqual([]);
});
it("rejects traversal, symbolic link files and substituted directories without touching their targets", async () => {
  const { root, store } = await fixture(), other = await fixture(), key = "avatars/avatar_12345678";
  await writeFile(join(other.root, "private"), data);
  await symlink(join(other.root, "private"), join(root, `${key}.blob`));
  await writeFile(join(root, `${key}.json`), JSON.stringify(legacyMetadata));
  await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
  await expect(store.putBytes(key, data, metadata)).rejects.toThrow("Object storage is unavailable");
  await store.delete(key);
  expect(await readFile(join(other.root, "private"))).toEqual(data);
  for (const invalid of ["../private", "avatars/../../private", "library//12345678", "avatars/12345678\0", "library/12345678.json"]) {
    await expect(store.getBytes(invalid, 100)).rejects.toThrow("Object storage is unavailable");
    await expect(store.putBytes(invalid, data, metadata)).rejects.toThrow("Object storage is unavailable");
    await expect(store.delete(invalid)).rejects.toThrow("Object storage is unavailable");
  }
  await rm(join(root, "avatars"), { recursive: true });
  await symlink(other.root, join(root, "avatars"));
  await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
  await expect(store.putBytes(key, data, metadata)).rejects.toThrow("Object storage is unavailable");
  await expect(store.delete(key)).rejects.toThrow("Object storage is unavailable");
  await expect(createFilesystemByteStore(root)).rejects.toThrow("Object storage is unavailable");
});
it("bounds inputs and aborts before mutation while keeping existing objects readable", async () => {
  const { root, store } = await fixture(), key = "avatars/avatar_12345678";
  for (const changed of [{ ...metadata, sha256: "a".repeat(64) }, { ...metadata, byteSize: 1 }]) {
    await expect(store.putBytes(key, data, changed)).rejects.toThrow("Object storage is unavailable");
  }
  await expect(store.putBytes(key, data, metadata, AbortSignal.abort())).rejects.toThrow("Object storage is unavailable");
  expect(await readdir(join(root, "avatars"))).toEqual([]);
  await store.putBytes(key, data, metadata);
  for (const limit of [0, -1, NaN, 1.5, 16 * 1024 * 1024 + 1]) await expect(store.getBytes(key, limit)).rejects.toThrow("Object storage is unavailable");
  await expect(store.getBytes(key, 100, AbortSignal.abort())).rejects.toThrow("Object storage is unavailable");
  await expect(store.delete(key, AbortSignal.abort())).rejects.toThrow("Object storage is unavailable");
  expect(await store.getBytes(key, 100)).toEqual(data);
});
it("rejects non-regular files and symbolic link metadata", async () => {
  const { root, store } = await fixture(), key = "avatars/avatar_12345678";
  await mkdir(join(root, `${key}.blob`));
  await writeFile(join(root, `${key}.json`), JSON.stringify(legacyMetadata));
  await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
  await unlink(join(root, `${key}.json`));
  await writeFile(join(root, "metadata"), JSON.stringify(legacyMetadata));
  await symlink(join(root, "metadata"), join(root, `${key}.json`));
  await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
});
it("selects explicit or legacy filesystem settings consistently and rejects hosted production local storage", () => {
  const selfHosted = { environment: "production", deployment: "self_hosted" } as const;
  expect(storageBackend({})).toBe("s3");
  expect(loadFilesystemConfig({ MISTY_LIBRARY_LOCAL_DIR: " /old " }, selfHosted)).toBe("/old");
  expect(loadFilesystemConfig({ MISTY_LIBRARY_FILESYSTEM_DIR: "/new", MISTY_LIBRARY_LOCAL_DIR: "/old" }, selfHosted)).toBe("/new");
  expect(loadFilesystemConfig({ MISTY_LIBRARY_BACKEND: " S3 ", MISTY_LIBRARY_LOCAL_DIR: "/old" }, selfHosted)).toBeNull();
  expect(loadFilesystemConfig({ MISTY_LIBRARY_BACKEND: " FileSystem ", MISTY_LIBRARY_FILESYSTEM_DIR: "/new" }, selfHosted)).toBe("/new");
  expect(() => loadFilesystemConfig({ MISTY_LIBRARY_BACKEND: "filesystem" }, selfHosted)).toThrow("valid directory");
  expect(() => storageBackend({ MISTY_LIBRARY_BACKEND: "unknown" })).toThrow("Unsupported library storage backend");
  expect(() => loadFilesystemConfig({ MISTY_LIBRARY_LOCAL_DIR: "/old" }, { ...selfHosted, deployment: "hosted" })).toThrow("requires a self-hosted deployment");
});
