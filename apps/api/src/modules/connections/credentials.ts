import { createCipheriv, createDecipheriv, randomBytes } from "node:crypto";

// A refresh response can retain the previous refresh token alongside its new
// access/identity tokens. Bound that combined private envelope separately.
export const connectionCredentialMaxBytes = 2 * 1024 * 1024;

/** Existing Go AES-256-GCM format: ciphertext followed by a 16-byte tag. */
export function createConnectionCipher(key: Uint8Array) {
  if (key.byteLength !== 32) throw new Error("Connection encryption requires a 32-byte key");
  const privateKey = Buffer.from(key);
  const decrypt = (aad: string, ciphertext: Buffer, nonce: Buffer, keyVersion: number) => {
    if (keyVersion !== 1 || nonce.length !== 12 || ciphertext.length < 16 || ciphertext.length > connectionCredentialMaxBytes + 16) throw new Error("Invalid connection credential");
    const decipher = createDecipheriv("aes-256-gcm", privateKey, nonce);
    decipher.setAAD(Buffer.from(aad));
    decipher.setAuthTag(ciphertext.subarray(-16));
    return Buffer.concat([decipher.update(ciphertext.subarray(0, -16)), decipher.final()]);
  };
  const encrypt = (aad: string, plaintext: Uint8Array) => {
    if (plaintext.byteLength > connectionCredentialMaxBytes) throw new Error("Connection credential is too large");
    const nonce = randomBytes(12), cipher = createCipheriv("aes-256-gcm", privateKey, nonce);
    cipher.setAAD(Buffer.from(aad));
    return { ciphertext: Buffer.concat([cipher.update(plaintext), cipher.final(), cipher.getAuthTag()]), nonce, keyVersion: 1 };
  };
  return {
    encrypt(provider: string, plaintext: Uint8Array) { return encrypt(`misty-connected-account-v1:${provider}`, plaintext); },
    encryptLegacy(provider: string, plaintext: Uint8Array) { return encrypt(`misty-provider-v2:${provider}`, plaintext); },
    decrypt(provider: string, ciphertext: Buffer, nonce: Buffer, keyVersion: number) {
      return decrypt(`misty-connected-account-v1:${provider}`, ciphertext, nonce, keyVersion);
    },
    decryptLegacy(provider: string, ciphertext: Buffer, nonce: Buffer, keyVersion: number) {
      try { return decrypt(`misty-provider-v2:${provider}`, ciphertext, nonce, keyVersion); }
      catch (error) {
        // Go retained this one metadata rename. Never try unrelated providers or
        // the connected-account AAD as a fallback for a legacy credential.
        if (provider !== "google") throw error;
        return decrypt("misty-provider-v2:google_calendar", ciphertext, nonce, keyVersion);
      }
    },
  };
}
export type ConnectionCipher = ReturnType<typeof createConnectionCipher>;

export function loadConnectionCipher(value: string | undefined): ConnectionCipher | null {
  const raw = value?.trim();
  if (!raw) return null;
  // Match Go's base64, hexadecimal, then literal UTF-8 precedence. Node's base64
  // decoder alone is permissive, so validate the alphabet/padding first.
  const base64 = raw.replace(/[\r\n]/g, "");
  if (/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(base64)) {
    const key = Buffer.from(base64, "base64");
    if (key.length === 32) return createConnectionCipher(key);
  }
  if (/^[a-f\d]{64}$/i.test(raw)) return createConnectionCipher(Buffer.from(raw, "hex"));
  if (Buffer.byteLength(raw) === 32) return createConnectionCipher(Buffer.from(raw));
  throw new Error("SPACE_LINK_ENCRYPTION_KEY must contain 32 bytes, base64, or hexadecimal");
}
