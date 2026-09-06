import { readFile } from "node:fs/promises";
import { expect, it, vi } from "vitest";
import { createConnectionCipher, loadConnectionCipher } from "../../../connections/credentials.js";
import { ProviderCleanupError, withDeletionCredential } from "./credentials.js";
const fixtures = JSON.parse(await readFile(new URL("../../../../../../../docs/migration/fixtures/legacy-provider-credentials.json", import.meta.url), "utf8")) as Array<{ provider: string; aadProvider: string; key: string; nonce: string; ciphertext: string; plaintext: string }>;
it("reads Go-compatible legacy nested/flat tokens, custom clients and the Google Calendar metadata rename", async () => {
  for (const f of fixtures) {
    const cipher = loadConnectionCipher(f.key)!, input = { format: "legacy" as const, provider: f.provider, keyVersion: 1, ciphertext: Buffer.from(f.ciphertext, "base64"), nonce: Buffer.from(f.nonce, "base64") };
    expect(cipher.decryptLegacy(f.provider, input.ciphertext, input.nonce, 1).toString()).toBe(f.plaintext);
    const raw = JSON.parse(f.plaintext), expected = raw.Token ?? raw;
    expect(await withDeletionCredential(cipher, input, async token => token)).toEqual({ accessToken: expected.access_token, refreshToken: expected.refresh_token ?? "",
      customClient: raw.Custom ? { clientId: raw.ClientID, clientSecret: raw.ClientSecret } : null });
    expect(() => cipher.decrypt(f.provider, input.ciphertext, input.nonce, 1)).toThrow();
    await expect(withDeletionCredential(cipher, { ...input, provider: `${f.provider}-wrong` }, async () => true)).rejects.toMatchObject({ code: "provider_credential_unreadable" });
    const tampered = Buffer.from(input.ciphertext); tampered[0] = tampered[0]! ^ 1;
    await expect(withDeletionCredential(cipher, { ...input, ciphertext: tampered }, async () => true)).rejects.toBeInstanceOf(ProviderCleanupError);
    await expect(withDeletionCredential(cipher, { ...input, keyVersion: 2 }, async () => true)).rejects.toBeInstanceOf(ProviderCleanupError);
  }
});
it("reads connected-account tokens without falling back to legacy AAD and keeps retry material intact", async () => {
  const cipher = createConnectionCipher(Buffer.alloc(32, 31)), encrypted = cipher.encrypt("google", Buffer.from('{"access_token":"access","refresh_token":"refresh"}'));
  const input = { format: "connected" as const, provider: "google", ...encrypted }, before = Buffer.from(encrypted.ciphertext);
  expect(await withDeletionCredential(cipher, input, async token => token)).toEqual({ accessToken: "access", refreshToken: "refresh", customClient: null });
  await expect(withDeletionCredential(cipher, { ...input, format: "legacy" }, async () => true)).rejects.toBeInstanceOf(ProviderCleanupError);
  const failure = new ProviderCleanupError("provider_cleanup_unavailable");
  await expect(withDeletionCredential(cipher, input, async () => { throw failure; })).rejects.toBe(failure);
  expect(encrypted.ciphertext).toEqual(before);
});
it("rejects malformed/absent tokens and invalid encrypted envelopes without invoking a provider", async () => {
  const cipher = createConnectionCipher(Buffer.alloc(32, 31)), effect = vi.fn();
  for (const value of ["{}", "null", "not-json", '{"access_token":123}', '{"access_token":"bad\\r\\nvalue"}', '{"Token":{"access_token":"wrong-format"}}']) {
    const encrypted = cipher.encrypt("google", Buffer.from(value));
    await expect(withDeletionCredential(cipher, { format: "connected", provider: "google", ...encrypted }, effect)).rejects.toBeInstanceOf(ProviderCleanupError);
  }
  const encrypted = cipher.encrypt("google", Buffer.from('{"access_token":"access"}'));
  for (const changed of [{ nonce: Buffer.alloc(11) }, { ciphertext: Buffer.alloc(2 * 1024 * 1024 + 17) }]) {
    await expect(withDeletionCredential(cipher, { format: "connected", provider: "google", ...encrypted, ...changed }, effect)).rejects.toBeInstanceOf(ProviderCleanupError);
  }
  expect(effect).not.toHaveBeenCalled();
});
it("zeros the decrypted buffer before provider I/O without mutating stored ciphertext", async () => {
  const cipher = createConnectionCipher(Buffer.alloc(32, 31)), plaintext = Buffer.from('{"access_token":"access"}');
  const encrypted = cipher.encrypt("google", plaintext);
  const observed = { ...cipher, decrypt: () => plaintext };
  await withDeletionCredential(observed, { format: "connected", provider: "google", ...encrypted }, async tokens => {
    expect(plaintext.every(byte => byte === 0)).toBe(true); expect(tokens.accessToken).toBe("access");
  });
});
