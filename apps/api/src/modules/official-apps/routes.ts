import { randomBytes } from "node:crypto";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { requireAccount, type AccountEnvironment } from "../auth/account-session.js";
import { hashToken, type AuthService } from "../auth/service.js";
import type { createOfficialCatalog } from "./catalog.js";
import { InstallationError, type createInstallationRepository } from "./repository.js";

export function createOfficialAppRoutes(options: { auth: AuthService; catalog: ReturnType<typeof createOfficialCatalog>; repository: ReturnType<typeof createInstallationRepository> }) {
  const app = new Hono<AccountEnvironment>(), { repository, catalog } = options;
  app.onError((error, c) => {
    if (error instanceof InstallationError) {
      if (error.code === "app_not_found") return c.json({ code: "app_not_installed" }, 404);
      if (error.code === "account_unavailable") return c.json({ code: "not_authenticated" }, 401);
      return c.json({ code: error.code }, error.code === "app_runtime_forbidden" ? 403 : 409);
    }
    throw error;
  });
  for (const path of ["/apps", "/apps/:appID", "/me/apps", "/me/apps/:appID", "/me/apps/:appID/sessions"]) {
    app.use(path, requireAccount(options.auth));
    app.use(path, bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  app.get("/apps", (c) => c.json({ apps: catalog.all(), host_protocol_version: catalog.hostProtocol }));
  app.get("/apps/:appID", (c) => { const item = catalog.find(c.req.param("appID")); return item ? c.json(item) : c.json({ code: "app_not_found" }, 404); });
  app.get("/me/apps", async (c) => c.json({ apps: (await repository.list(c.get("account").id)).filter((item) => catalog.find(item.app_id)) }));
  app.put("/me/apps/:appID", async (c) => {
    const item = catalog.find(c.req.param("appID"));
    if (!item) return c.json({ code: "app_not_found" }, 404);
    const body = z.object({ permission_version: z.number().int().nullable().optional().transform((value) => value ?? 0) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    if (body.data.permission_version !== item.permission_version) return c.json({ code: "app_permissions_changed", message: "Review the app's current permissions before installing." }, 409);
    return c.json(await repository.install(c.get("account").id, item));
  });
  app.patch("/me/apps/:appID", async (c) => {
    const item = catalog.find(c.req.param("appID"));
    if (!item) return c.json({ code: "app_not_found" }, 404);
    const body = z.object({ pinned: z.boolean() }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await repository.pin(c.get("account").id, item.id, body.data.pinned));
  });
  app.delete("/me/apps/:appID", async (c) => {
    const item = catalog.find(c.req.param("appID"));
    return item ? c.json(await repository.uninstall(c.get("account").id, item.id)) : c.json({ code: "app_not_found" }, 404);
  });
  app.post("/me/apps/:appID/sessions", async (c) => {
    const item = catalog.find(c.req.param("appID"));
    if (!item) return c.json({ code: "app_not_found" }, 404);
    if (item.desktop.runtime === "embedded") return c.json({ code: "app_is_host_embedded", message: "This app runs inside the trusted Misty Host." }, 409);
    const body = z.object({ space_id: z.string().nullable().optional().transform((value) => value?.trim() ?? "") }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    const token = randomBytes(32).toString("base64url");
    return c.json({ token, ...await repository.session(c.get("account").id, item.id, hashToken(token), body.data.space_id), sdk_base_url: "/v1/app-runtime" }, 201);
  });
  return app;
}
