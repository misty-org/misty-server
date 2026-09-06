import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import type { AuthService } from "../auth/service.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AppSessionRevoked, type AppRuntimeRepository } from "../app-runtime/repository.js";
import type { SpaceActor } from "./access.js";
import { listTemplates } from "./templates.js";
import { SpaceError } from "./model.js";
import type { createSpaceRepository } from "./repository.js";
export type SpaceDependencies = { auth: AuthService; appRuntime: AppRuntimeRepository; repository: ReturnType<typeof createSpaceRepository>; providers?: { provider: string; configured: boolean }[] };
const string = z.string().nullable().optional().transform((value) => value ?? "");
export function createSpaceRoutes(options: SpaceDependencies) {
  const app = new Hono<{ Variables: { actor: SpaceActor } }>(), repository = options.repository;
  app.onError((error, c) => {
    if (error instanceof AppSessionRevoked) return c.json({ code: "app_session_expired" }, 401);
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "not_authenticated" ? 401 : error.code === "not_found" ? 404 : error.code === "forbidden" ? 403 : error.code === "invalid_request" ? 400 : 409);
    throw error;
  });
  for (const path of ["/spaces", "/spaces/:spaceID", "/spaces/:spaceID/setup", "/spaces/:spaceID/members", "/spaces/:spaceID/members/:userID", "/spaces/:spaceID/leave", "/spaces/:spaceID/transfer", "/spaces/:spaceID/members/:userID/permissions", "/spaces/:spaceID/agents"]) {
    app.use(path, async (c, next) => {
      c.header("Cache-Control", "no-store");
      const token = sessionToken(c), account = token ? await options.auth.authenticate(token) : null;
      if (account) c.set("actor", { userId: account.id });
      else {
        if (!["/spaces/:spaceID", "/spaces/:spaceID/members"].includes(path) || !["GET", "HEAD"].includes(c.req.method)) return c.json({ code: "not_authenticated" }, 401);
        const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
        const session = bearer ? await options.appRuntime.findSession(hashToken(bearer)) : null;
        if (!session) return c.json({ code: "not_authenticated" }, 401);
        if (session.space_id !== c.req.param("spaceID") || !session.scopes.includes("spaces.read")) return c.json({ code: "app_scope_forbidden" }, 403);
        c.set("actor", { userId: session.user_id, appSession: session });
      }
      await next();
    });
    app.use(path, bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  app.get("/space-templates", (c) => c.json({ templates: listTemplates(), providers: options.providers ?? [{ provider: "github", configured: false }] }));
  app.get("/spaces", async (c) => c.json(await repository.list(c.get("actor").userId)));
  app.post("/spaces", async (c) => {
    const input = z.object({ name: string, template_id: string, integration_providers: z.array(z.string()).nullable().optional().transform((value) => value ?? []) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await repository.create(c.get("actor").userId, input.data, c.req.header("Idempotency-Key") ?? ""), 201);
  });
  app.get("/spaces/:spaceID", async (c) => c.json(await repository.get(c.get("actor"), c.req.param("spaceID"))));
  app.delete("/spaces/:spaceID", async (c) => {
    const input = z.object({ confirmation: string }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    await repository.delete(c.get("actor"), c.req.param("spaceID"), input.data.confirmation); return c.body(null, 204);
  });
  app.on(["PUT", "PATCH"], "/spaces/:spaceID", async (c) => {
    const input = z.object({ name: string }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await repository.rename(c.get("actor"), c.req.param("spaceID"), input.data.name));
  });
  app.get("/spaces/:spaceID/members", async (c) => c.json(await repository.members(c.get("actor"), c.req.param("spaceID"))));
  app.delete("/spaces/:spaceID/members/:userID", async (c) => {
    await repository.removeMember(c.get("actor"), c.req.param("spaceID"), c.req.param("userID")); return c.body(null, 204);
  });
  app.post("/spaces/:spaceID/leave", async (c) => {
    await repository.leave(c.get("actor"), c.req.param("spaceID")); return c.body(null, 204);
  });
  app.post("/spaces/:spaceID/transfer", async (c) => {
    const input = z.object({ user_id: z.string().min(1).max(200) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    await repository.transfer(c.get("actor"), c.req.param("spaceID"), input.data.user_id); return c.body(null, 204);
  });
  app.get("/spaces/:spaceID/agents", async (c) => c.json(await repository.agents(c.get("actor"), c.req.param("spaceID"))));
  app.get("/spaces/:spaceID/members/:userID/permissions", async (c) => c.json(await repository.permissions(c.get("actor"), c.req.param("spaceID"), c.req.param("userID"))));
  app.put("/spaces/:spaceID/members/:userID/permissions", async (c) => {
    const input = z.object({ permission: z.string(), effect: z.string() }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await repository.permissions(c.get("actor"), c.req.param("spaceID"), c.req.param("userID"), input.data));
  });
  app.get("/spaces/:spaceID/setup", async (c) => c.json(await repository.setup(c.get("actor"), c.req.param("spaceID"))));
  app.patch("/spaces/:spaceID/setup", async (c) => {
    const input = z.object({ provider: z.string(), status: z.string() }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await repository.setup(c.get("actor"), c.req.param("spaceID"), input.data));
  });
  return app;
}
