export const sessionTtlSeconds = 30 * 24 * 60 * 60;
export const sessionCookieName = "misty_session";
export type AuthUser = { id: string; license_id: string; name: string; username: string; email: string };
export class AuthRejected extends Error {}
export class AuthBusy extends Error {}
export class AccountConflict extends Error {
  constructor(readonly field: "email" | "username") { super(field === "email" ? "email already registered" : "username already taken"); }
}
export class SelfHostProofRequired extends Error {}
export class SelfHostSubjectMismatch extends Error {}
export function normalizeUsername(value: string) {
  const username = value.trim().toLowerCase();
  if (!/^[a-z0-9_]{3,30}$/.test(username)) throw new Error("username must be 3-30 lowercase letters, numbers, or underscores");
  return username;
}
