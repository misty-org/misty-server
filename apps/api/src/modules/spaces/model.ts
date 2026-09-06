export class SpaceError extends Error {
  constructor(readonly code: "invalid_request" | "not_found" | "forbidden" | "not_authenticated" | "default_space_protected" | "space_ownership_limit_reached" | "version_conflict" | "onboarding_already_complete" | "onboarding_request_changed") { super(code); }
}
// Match Go strings.TrimSpace, including U+0085 and excluding the BOM.
export function trimSpace(value: string) { return value.replace(/^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g, ""); }
export function normalizeSpaceName(name: string) {
  const value = trimSpace(name); if ([...value].length < 1 || [...value].length > 80) throw new SpaceError("invalid_request"); return value;
}
export function clientInteger(value: string | number | bigint) {
  const number = Number(value); if (!Number.isSafeInteger(number)) throw new Error("Space integer exceeds supported client precision"); return number;
}
