import type { Environment } from "../../../../../../packages/runtime/src/config.js";

export function loadRecoveryConfig(env: Environment) {
  function url(name: string, fallback: string) {
    const parsed = new URL(env[name]?.trim() || fallback);
    if (parsed.username || parsed.password || (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname)))) throw new Error(`Invalid ${name}`);
    return parsed;
  }
  const start = url("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start");
  if (start.hash || start.search) throw new Error("PASSWORD_RESET_START_URL must not contain a query or fragment");
  return { startUrl: start.href, redirectUrl: url("PASSWORD_RESET_URL", "http://localhost:5173/#/reset").href };
}
export type RecoveryConfig = ReturnType<typeof loadRecoveryConfig>;
