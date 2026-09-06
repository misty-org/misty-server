import { readFile } from "node:fs/promises";
import { beforeAll, expect, it } from "vitest";
import { createPasswordHasher, type PasswordHasher } from "./passwords.js";

const fixtures = JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/auth-passwords.json", import.meta.url), "utf8")) as Array<{ password: string; hash: string; extendedMatches: boolean }>;
let passwords: PasswordHasher;
beforeAll(async () => { passwords = await createPasswordHasher(); });
it.each(fixtures)("verifies the Go/Node fixture $hash with identical UTF-8 and truncation behavior", async (fixture) => {
  expect(await passwords.verify(fixture.password, fixture.hash)).toBe(true);
  expect(await passwords.verify(fixture.password + "suffix", fixture.hash)).toBe(fixture.extendedMatches);
  expect(await passwords.verify("wrong-test-password", fixture.hash)).toBe(false);
});
it("rejects oversized passwords before hashing and performs a safe comparison for absent/corrupt hashes", async () => {
  await expect(passwords.hash("🔒".repeat(19))).rejects.toThrow("byte limit");
  expect(await passwords.verify("test", null)).toBe(false);
  expect(await passwords.verify("test", "not-a-hash")).toBe(false);
});
it("bounds concurrent password work rather than accumulating an unbounded worker queue", async () => {
  const limited = await createPasswordHasher({ concurrency: 1, maxQueued: 0 });
  const first = limited.hash("test-password");
  await expect(limited.hash("another-password")).rejects.toThrow("queue is full");
  await first;
  expect(await limited.verify("test", null)).toBe(false);
});
