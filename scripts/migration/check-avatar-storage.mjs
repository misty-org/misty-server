// Cross-runtime rollback proof using only a disposable local directory.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createHash } from "node:crypto";
import { mkdtemp, rm, writeFile, readdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFilesystemByteStore } from "../../dist/apps/api/src/modules/storage/filesystem-store.js";

const root = await mkdtemp(join(tmpdir(), "misty-avatar-compat-"));
try {
  const store = await createFilesystemByteStore(root), data = Buffer.from("Native local image bytes");
  await writeFile(join(root, "native.expected"), data, { mode: 0o600 });
  for (const key of ["avatars/avatar_12345678", "library/native_12345678"]) {
    await store.putBytes(key, data, { byteSize: data.length, sha256: createHash("sha256").update(data).digest("hex"), mimeType: "image/png" });
  }
  await promisify(execFile)("go", ["test", "./test/contract/http/api", "-run", "^TestNativeAvatarFilesystemRoundTrip$", "-count=1"], {
    cwd: new URL("../../", import.meta.url), env: { ...process.env, MISTY_TEST_AVATAR_DIRECTORY: root }, timeout: 120000,
  });
  for (const key of ["avatars/user_12345678", "library/legacy_12345678"]) {
    assert.deepEqual(await store.getBytes(key, 100), Buffer.from("Go local image bytes"));
    await store.delete(key);
  }
  for (const key of ["avatars/avatar_12345678", "library/native_12345678"]) await store.delete(key);
  assert.deepEqual(await readdir(join(root, "avatars")), []);
  console.log("Native-to-Go and Go-to-native private filesystem reads, checksums and cleanup passed for avatar and Library layouts.");
} finally { await rm(root, { recursive: true, force: true }); }
