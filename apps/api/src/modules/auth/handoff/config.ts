import type { Environment } from "../../../../../../packages/runtime/src/config.js";

const paths = new Set(["/settings", "/settings/account", "/settings/usage", "/settings/billing", "/settings/privacy"]);
export function normalizeHandoffPath(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed || trimmed === "/") return "/settings";
  const path = trimmed.replace(/\/$/, "");
  return paths.has(path) ? path : null;
}

export function loadHandoffConfig(env: Environment) {
  function validated(name: string, fallback: string) {
    const url = new URL(env[name] || fallback);
    const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
    if ((url.protocol !== "https:" && !(url.protocol === "http:" && local)) || url.username || url.password || url.search || url.hash) {
      throw new Error(`${name} must be an HTTPS URL or a local development URL without credentials, query or fragment`);
    }
    return url;
  }
  const website = validated("MISTY_WEBSITE_URL", "http://localhost:5174");
  if (website.pathname !== "/") throw new Error("MISTY_WEBSITE_URL must be an origin");
  return Object.freeze({ startUrl: validated("AUTH_HANDOFF_START_URL", "http://localhost:8080/auth/handoff/start").href, websiteUrl: website.origin });
}
export type HandoffConfig = ReturnType<typeof loadHandoffConfig>;
