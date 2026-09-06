import { readFile } from "node:fs/promises";
import { expect, it } from "vitest";
import { createConnectionCipher, loadConnectionCipher } from "./credentials.js";
import { loadConnectionProviders } from "./config.js";

it("reads the same provider-bound AES-GCM fixtures verified by Go", async () => {
  const fixtures = JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/connection-credentials.json", import.meta.url), "utf8")) as Array<{ provider: string; key: string; nonce: string; ciphertext: string; plaintext: string }>;
  for (const fixture of fixtures) {
    const cipher = loadConnectionCipher(fixture.key)!, encrypted = Buffer.from(fixture.ciphertext, "base64"), nonce = Buffer.from(fixture.nonce, "base64");
    expect(cipher.decrypt(fixture.provider, encrypted, nonce, 1).toString()).toBe(fixture.plaintext);
    expect(() => cipher.decrypt(`${fixture.provider}-wrong`, encrypted, nonce, 1)).toThrow();
    const altered = Buffer.from(encrypted); altered[0] = altered[0]! ^ 1;
    expect(() => cipher.decrypt(fixture.provider, altered, nonce, 1)).toThrow();
    expect(() => cipher.decrypt(fixture.provider, encrypted, nonce, 2)).toThrow();
    const fresh = cipher.encrypt(fixture.provider, Buffer.from(fixture.plaintext));
    expect(fresh.nonce).not.toEqual(nonce); expect(cipher.decrypt(fixture.provider, fresh.ciphertext, fresh.nonce, fresh.keyVersion).toString()).toBe(fixture.plaintext);
  }
});

it("accepts Go key encodings and rejects malformed or oversized encrypted inputs", () => {
  const key = Buffer.from("x".repeat(32)), source = createConnectionCipher(key).encrypt("google", Buffer.from("private"));
  for (const encoded of [key.toString(), key.toString("hex"), key.toString("base64"), `${key.toString("base64").slice(0, 20)}\n${key.toString("base64").slice(20)}`]) {
    const cipher = loadConnectionCipher(encoded)!;
    expect(cipher.decrypt("google", source.ciphertext, source.nonce, 1).toString()).toBe("private");
    expect(() => cipher.decrypt("google", source.ciphertext, Buffer.alloc(11), 1)).toThrow();
    expect(() => cipher.decrypt("google", Buffer.alloc(15), source.nonce, 1)).toThrow();
    expect(() => cipher.decrypt("google", Buffer.alloc(2 * 1024 * 1024 + 17), source.nonce, 1)).toThrow();
    expect(() => cipher.encrypt("google", Buffer.alloc(2 * 1024 * 1024 + 1))).toThrow();
  }
  expect(loadConnectionCipher(" ")).toBeNull();
  for (const bad of ["short", `${key.toString("base64")}!!!`, "é".repeat(32)]) expect(() => loadConnectionCipher(bad)).toThrow();
  expect(() => createConnectionCipher(Buffer.alloc(31))).toThrow();
  expect(loadConnectionProviders({ GOOGLE_CLIENT_ID: "present", GOOGLE_CLIENT_SECRET: " present ", FIGMA_CLIENT_ID: "only-id", MICROSOFT_CLIENT_SECRET: "secret-only" }))
    .toEqual({ google: true, microsoft: false, figma: false, dropbox: false, discord: false, instagram: false });
});
