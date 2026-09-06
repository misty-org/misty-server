import { Hono } from "hono";
import { html } from "hono/html";
import { bodyLimit } from "hono/body-limit";
import { createAdmission } from "../../../../../packages/runtime/src/admission.js";
import { IdentifierSchema } from "@misty/contracts";
import type { AuthService } from "../auth/service.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AppSessionRevoked, type AppRuntimeRepository } from "../app-runtime/repository.js";
import type { SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import { ConnectionUnavailable, type createConnectionRepository } from "./repository.js";
import { ConnectionAuthorizationError } from "./oauth/catalog.js";
import type { ConnectionAuthorizationService } from "./oauth/service.js";
import type { createIntegrationRepository } from "./integrations.js";

export const connectionRpcMethods = new Set(["connections.list", "connections.remove", "connections.authorize", "integrations.list", "integrations.bind"]);
export function createConnectionRoutes(options: { auth: AuthService; appRuntime: AppRuntimeRepository; repository: ReturnType<typeof createConnectionRepository>; providers: Record<string, boolean>; authorization?: ConnectionAuthorizationService | null;
  integrations?: ReturnType<typeof createIntegrationRepository>; integrationProviders?: { provider: string; configured: boolean }[] }) {
  const app = new Hono<{ Variables: { actor: SpaceActor; accountSessionHash: string | undefined } }>();
  const callbackAdmission = createAdmission(8);
  app.onError((error, c) => {
    if (error instanceof AppSessionRevoked) return c.json({ code: "app_session_expired" }, 401);
    if (error instanceof ConnectionUnavailable) return c.json({ code: "connections_not_configured" }, 503);
    if (error instanceof ConnectionAuthorizationError) return c.json({ code: error.code }, error.code === "authorization_limit" ? 429 : error.code === "provider_not_configured" || error.code === "authorization_unavailable" ? 503 : 400);
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "not_authenticated" ? 401 : error.code === "not_found" ? 404 : error.code === "forbidden" ? 403 : 400);
    throw error;
  });
  for (const path of ["/connections", "/connections/:connectionID", "/connections/:provider/authorize", "/spaces/:spaceID/integrations", "/spaces/:spaceID/integrations/:provider/bind"]) app.use(path, async (c, next) => {
    c.header("Cache-Control", "no-store");
    const token = sessionToken(c), account = token ? await options.auth.authenticate(token) : null;
    if (account) { c.set("actor", { userId: account.id }); c.set("accountSessionHash", hashToken(token!)); }
    else {
      const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
      const session = bearer ? await options.appRuntime.findSession(hashToken(bearer)) : null;
      if (!session) return c.json({ code: "not_authenticated" }, 401);
      if (path.startsWith("/spaces/") && session.space_id !== c.req.param("spaceID")) return c.json({ code: "app_scope_forbidden" }, 403);
      if (!session.scopes.includes(c.req.method === "GET" ? "connections.read" : "connections.write")) return c.json({ code: "app_scope_forbidden" }, 403);
      c.set("actor", { userId: session.user_id, appSession: session });
    }
    await next();
  });
  if (options.integrations) {
    const integrations = options.integrations;
    app.get("/spaces/:spaceID/integrations", async c => c.json({ integrations: await integrations.list(c.get("actor"), c.req.param("spaceID")), providers: options.integrationProviders ?? [] }));
    app.post("/spaces/:spaceID/integrations/:provider/bind", bodyLimit({ maxSize: 16384, onError: c => c.json({ code: "invalid_request" }, 400) }), async c =>
      c.json(await integrations.bind(c.get("actor"), c.req.param("spaceID"), c.req.param("provider"), await c.req.json().catch(() => null)), 201));
  }
  app.post("/connections/:provider/authorize", bodyLimit({ maxSize: 16384, onError: (c) => c.json({ code: "invalid_request" }, 400) }), async (c) => {
    if (!options.authorization) return c.json({ code: "connections_not_configured" }, 503);
    const actor = c.get("actor"), sessionHash = c.get("accountSessionHash");
    return c.json(await options.authorization.begin({ ...actor, ...(sessionHash ? { sessionHash } : {}) }, c.req.param("provider"), await c.req.json().catch(() => undefined)));
  });
  app.get("/oauth/connections/:provider/callback", async (c) => {
    c.header("Cache-Control", "no-store"); c.header("Referrer-Policy", "no-referrer");
    c.header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'");
    c.header("X-Frame-Options", "DENY");
    return callbackAdmission.run(async () => {
      try {
        if (!options.authorization) throw new ConnectionAuthorizationError("provider_not_configured");
        if (c.req.url.length > 16384) throw new ConnectionAuthorizationError("invalid_request");
        const result = await options.authorization.callback(c.req.param("provider"), new URL(c.req.url).searchParams, c.req.raw.signal);
        return c.html(html`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Account connected · Misty</title><body><main><h1>Account connected</h1><p>${result.display} is connected. Return to Misty to continue.</p></main></body></html>`);
      } catch {
        return c.html(html`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connection incomplete · Misty</title><body><main><h1>Connection incomplete</h1><p>Return to Misty and connect your account again.</p></main></body></html>`, 400);
      }
    }, () => { c.header("Retry-After", "5"); return c.text("Connection service is busy. Return to Misty and try again.", 503); });
  });
  app.get("/connections", async (c) => c.json({ connections: await options.repository.list(c.get("actor")), providers: options.providers }));
  app.delete("/connections/:connectionID", async (c) => {
    const id = IdentifierSchema.safeParse(c.req.param("connectionID"));
    if (!id.success) return c.json({ code: "invalid_request" }, 400);
    c.header("X-Misty-Provider-Revocation", await options.repository.remove(c.get("actor"), id.data, c.req.raw.signal));
    return c.body(null, 204);
  });
  return app;
}
