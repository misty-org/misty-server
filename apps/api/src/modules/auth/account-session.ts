import { createMiddleware } from "hono/factory";
import type { AuthUser } from "./model.js";
import type { AuthService } from "./service.js";
import { sessionToken } from "./cookies.js";

export type AccountEnvironment = { Variables: { account: AuthUser } };
/** Account routers must never interpret app credentials as full account authority. */
export function requireAccount(service: AuthService) {
  return createMiddleware<AccountEnvironment>(async (c, next) => {
    c.header("Cache-Control", "no-store");
    const token = sessionToken(c), user = token ? await service.authenticate(token) : null;
    if (!user) return c.json({ code: "not_authenticated" }, 401);
    c.set("account", user);
    await next();
  });
}
