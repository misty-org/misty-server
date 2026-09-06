import type { Environment } from "../../../../../../packages/runtime/src/config.js";

export function loadConnectionAuthorizationConfig(env: Environment) {
  const raw = env.MISTY_PUBLIC_API_URL?.trim();
  if (!raw) return null;
  const url = new URL(raw), local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if ((url.protocol !== "https:" && !(url.protocol === "http:" && local)) || url.username || url.password || url.search || url.hash || !["/", "/api", "/api/", "/v1", "/v1/"].includes(url.pathname)) {
    throw new Error("MISTY_PUBLIC_API_URL must be HTTPS (or loopback HTTP), with an optional /api or /v1 path and no credentials, query or fragment");
  }
  // Preserve Go's origin-only /api compatibility. Never derive callbacks from Host/proxy headers.
  return { apiBase: url.origin + (url.pathname === "/" ? "/api" : url.pathname.replace(/\/$/, "")) };
}
export type ConnectionAuthorizationConfig = NonNullable<ReturnType<typeof loadConnectionAuthorizationConfig>>;
