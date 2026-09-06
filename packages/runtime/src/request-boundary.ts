import { BlockList, isIP } from "node:net";
import type { Context } from "hono";
import { getConnInfo } from "@hono/node-server/conninfo";

const defaults = ["tauri://localhost", "http://tauri.localhost", "https://tauri.localhost", "http://localhost:5173", "http://127.0.0.1:5173", "https://apps.mistysys.com"];
const proxyDefaults = ["127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fe80::/10", "fc00::/7"];
const allowedHeaders = ["Accept", "Authorization", "Content-Type", "Idempotency-Key", "X-Request-ID", "X-Misty-Platform", "X-Misty-Release-Channel",
  "X-Misty-Session-Id", "X-Misty-Analytics-Enabled", "X-Misty-Device-Timestamp", "X-Misty-Device-Nonce", "X-Misty-Device-Signature",
  "X-Misty-Attachment-Upload-Token", "X-Misty-Library-Upload-Token", "X-Misty-Library-Reauthentication", "X-Misty-Self-Hosted-Entitlement"];

export function createRequestBoundary(options: { allowedOrigins?: string[]; trustProxyHeaders?: boolean; trustedProxyCidrs?: string[];
  peerAddress?: (c: Context) => string }) {
  const origins = [...defaults, ...(options.allowedOrigins ?? [])].map((value) => value.trim().toLowerCase());
  for (const origin of origins) {
    const url = new URL(origin);
    if (origin.includes("*") || url.username || url.password || (url.pathname && url.pathname !== "/") || url.search || url.hash || !["http:", "https:", "tauri:"].includes(url.protocol)) throw new Error("Invalid allowed origin");
  }
  const proxies = new BlockList();
  for (const definition of [...proxyDefaults, ...(options.trustedProxyCidrs ?? [])]) {
    const parts = definition.trim().split("/");
    const [address, bits] = parts;
    if (parts.length > 2 || (bits !== undefined && !/^\d+$/.test(bits))) throw new Error("Invalid trusted proxy prefix");
    const family = address ? isIP(address) : 0;
    if (!family || !address) throw new Error("Invalid trusted proxy address");
    const prefix = bits === undefined ? (family === 4 ? 32 : 128) : Number(bits);
    if (!Number.isInteger(prefix) || prefix < 0 || prefix > (family === 4 ? 32 : 128)) throw new Error("Invalid trusted proxy prefix");
    proxies.addSubnet(address, prefix, family === 4 ? "ipv4" : "ipv6");
  }
  const trusted = (address: string) => { const family = isIP(address); return Boolean(family && proxies.check(address, family === 4 ? "ipv4" : "ipv6")); };
  const peer = options.peerAddress ?? ((c: Context) => { try { return getConnInfo(c).remote.address ?? "unknown"; } catch { return "unknown"; } });
  function isAllowedOrigin(origin: string) {
    if (origins.includes(origin.toLowerCase())) return true;
    try {
      const url = new URL(origin);
      const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
      return url.protocol === "http:" && local && !url.username && !url.password && url.pathname === "/" && !url.search && !url.hash && Number(url.port) >= 5173 && Number(url.port) <= 5222;
    } catch { return false; }
  }
  return {
    isAllowedOrigin,
    clientIp(c: Context) {
      const remote = peer(c);
      if (!options.trustProxyHeaders || !trusted(remote)) return remote;
      const chain = c.req.header("X-Forwarded-For")?.split(",") ?? [];
      if (chain.length > 32) return remote;
      for (let index = chain.length - 1; index >= 0; index--) {
        const candidate = chain[index]!.trim();
        if (!isIP(candidate)) return remote;
        if (!trusted(candidate)) return candidate;
      }
      const real = c.req.header("X-Real-IP")?.trim();
      return real && isIP(real) ? real : remote;
    },
    secure(c: Context) {
      return new URL(c.req.url).protocol === "https:" || Boolean(options.trustProxyHeaders && trusted(peer(c)) && c.req.header("X-Forwarded-Proto")?.toLowerCase() === "https");
    },
    async middleware(c: Context, next: () => Promise<void>) {
      const origin = c.req.header("Origin");
      const allowed = origin ? isAllowedOrigin(origin) || origin === new URL(c.req.url).origin : false;
      if (origin && allowed) {
        c.header("Access-Control-Allow-Origin", origin);
        c.header("Access-Control-Allow-Credentials", "true");
        c.header("Access-Control-Expose-Headers", "X-Misty-Signed-Download, X-Request-ID");
        c.header("Vary", "Origin", { append: true });
      }
      const unsafe = !["GET", "HEAD", "OPTIONS"].includes(c.req.method);
      if ((origin && !allowed && (unsafe || c.req.method === "OPTIONS")) || (!origin && unsafe && c.req.header("Sec-Fetch-Site") === "cross-site")) {
        return c.json({ code: "origin_forbidden" }, 403);
      }
      if (c.req.method === "OPTIONS" && origin) {
        const headers = c.req.header("Access-Control-Request-Headers")?.split(",").map((value) => value.trim().toLowerCase()) ?? [];
        const method = c.req.header("Access-Control-Request-Method") ?? "";
        if (!headers.every((header) => allowedHeaders.some((allowed) => allowed.toLowerCase() === header)) || !["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"].includes(method)) return c.body(null, 403);
        c.header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS");
        c.header("Access-Control-Allow-Headers", allowedHeaders.join(", "));
        c.header("Access-Control-Max-Age", "300");
        c.header("Vary", "Access-Control-Request-Method, Access-Control-Request-Headers", { append: true });
        return c.body(null, 204);
      }
      await next();
    },
  };
}
export type RequestBoundary = ReturnType<typeof createRequestBoundary>;
