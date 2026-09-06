import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { loadRecoveryTokenKeys, recoveryToken } from "./token-keys.js";
import { loadRecoveryConfig } from "./config.js";

it("derives stable context-bound tokens and validates a rotating private key file", async () => {
  const directory = await mkdtemp(join(tmpdir(), "misty-recovery-keys-")), file = join(directory, "keys.json");
  const old = Buffer.alloc(32, 15), current = Buffer.alloc(32, 16);
  const document = { active: "current", keys: [{ id: "old", key: old.toString("base64") }, { id: "current", key: current.toString("base64") }] };
  try {
    await writeFile(file, JSON.stringify(document), { mode: 0o600 });
    const keys = await loadRecoveryTokenKeys(file), token = recoveryToken(keys, "old", "job", "person@example.invalid");
    expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(recoveryToken(keys, "old", "job", "person@example.invalid")).toBe(token);
    expect(recoveryToken(keys, "current", "job", "person@example.invalid")).not.toBe(token);
    expect(recoveryToken(keys, "old", "different-job", "person@example.invalid")).not.toBe(token);
    expect(recoveryToken(keys, "old", "job", "different-person@example.invalid")).not.toBe(token);
    for (const invalid of [{ ...document, active: "missing" }, { ...document, keys: [document.keys[0], document.keys[0]] }, { active: "bad", keys: [{ id: "bad", key: "short" }] }]) {
      await writeFile(file, JSON.stringify(invalid));
      await expect(loadRecoveryTokenKeys(file)).rejects.toThrow();
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});
it("preserves the reset fragment destination while rejecting unsafe start URLs", () => {
  expect(loadRecoveryConfig({}).redirectUrl).toBe("http://localhost:5173/#/reset");
  for (const url of ["http://remote.example/auth/reset/start", "https://user:secret@remote.example/auth/reset/start", "https://remote.example/auth/reset/start?token=old", "https://remote.example/#token"]) {
    expect(() => loadRecoveryConfig({ PASSWORD_RESET_START_URL: url })).toThrow();
  }
});
