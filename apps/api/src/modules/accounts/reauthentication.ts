import type { PasswordHasher } from "../auth/passwords.js";
export class AccountReauthenticationFailed extends Error {}
/** Verify outside row locks; the caller must compare this hash again under its
 * operation's account lock before relying on the password confirmation. */
export async function verifyAccountPassword(passwords: PasswordHasher, password: string, expectedHash: string, signal: AbortSignal) {
  signal.throwIfAborted();
  if (Buffer.byteLength(password) > 72 || !await passwords.verify(password, expectedHash)) throw new AccountReauthenticationFailed();
  signal.throwIfAborted();
}
