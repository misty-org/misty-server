import type { Context } from "hono";
import type { SelfHostAccess } from "./repository.js";
import { sessionToken } from "../auth/cookies.js";

const blocked = ["/ai", "/agents", "/billing", "/cloud", "/integrations", "/misty", "/provider-callbacks", "/runs", "/waitlist", "/auth/forgot", "/auth/reset", "/auth/handoff"];
const recovery = new Set(["/health", "/livez", "/readyz", "/instance", "/login", "/logout", "/self-host/bootstrap", "/self-host/enroll", "/self-host/entitlement"]);
export function publicPath(path: string) { return path.replace(/^\/(api|v1)(?=\/|$)/, "") || "/"; }
export function createSelfHostGate(options: { owner: (token: string) => Promise<string | null>; access: (userId: string) => Promise<SelfHostAccess | null>; now?: () => Date }) {
  return async (c: Context, next: () => Promise<void>) => {
    const path = publicPath(c.req.path);
    if (blocked.some((prefix) => path === prefix || path.startsWith(prefix + "/")) || path.includes("/integrations/") || path.includes("/provider-resources") || path.includes("/agents/")) {
      return c.json({ code: "feature_unavailable_self_hosted" }, 501);
    }
    if (!recovery.has(path)) {
      const token = sessionToken(c), userId = token ? await options.owner(token) : null;
      if (userId) {
        const access = await options.access(userId);
        if (!access || access.disabled_at || access.entitlement_expires_at <= (options.now?.() ?? new Date())) return c.json({ code: "self_host_entitlement_required", actions: ["retry_verification", "open_settings", "switch_hosted", "sign_out"] }, 402);
      }
    }
    await next();
  };
}
