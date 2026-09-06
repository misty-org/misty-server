import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { requireAccount, type AccountEnvironment } from "../../auth/account-session.js";
import type { AuthService } from "../../auth/service.js";
import { SpaceError, trimSpace } from "../model.js";
import { InvitationError, type createInvitationRepository } from "./repository.js";
export function createInvitationRoutes(options: { auth: AuthService; repository: ReturnType<typeof createInvitationRepository> }) {
  const app = new Hono<AccountEnvironment>(), repository = options.repository;
  app.onError((error, c) => {
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "forbidden" ? 403 : error.code === "not_authenticated" ? 401 : error.code === "not_found" ? 404 : error.code === "invalid_request" ? 400 : 409);
    if (error instanceof InvitationError) return c.json({ code: error.code === "invite_not_found" ? "not_found" : error.code }, error.code === "invite_expired" ? 410 : error.code === "invitation_unavailable" ? 503 : 404);
    throw error;
  });
  for (const path of ["/spaces/:spaceID/invitations", "/spaces/:spaceID/invitations/:inviteID", "/spaces/:spaceID/invitations/:inviteID/resend", "/spaces/invitations/:inviteID/accept", "/spaces/invitations/:inviteID/decline"]) {
    app.use(path, requireAccount(options.auth), bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  app.get("/spaces/:spaceID/invitations", async (c) => c.json(await repository.list(c.get("account").id, c.req.param("spaceID"))));
  app.post("/spaces/:spaceID/invitations", async (c) => {
    const input = z.object({ email: z.string().transform((value) => trimSpace(value).toLowerCase()).pipe(z.email().max(254)) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await repository.issue(c.get("account").id, c.req.param("spaceID"), input.data.email), 201);
  });
  app.post("/spaces/:spaceID/invitations/:inviteID/resend", async (c) => c.json(await repository.issue(c.get("account").id, c.req.param("spaceID"), null, c.req.param("inviteID"))));
  app.delete("/spaces/:spaceID/invitations/:inviteID", async (c) => { await repository.revoke(c.get("account").id, c.req.param("spaceID"), c.req.param("inviteID")); return c.body(null, 204); });
  for (const action of ["accept", "decline"] as const) app.post(`/spaces/invitations/:inviteID/${action}`, async (c) => {
    const result = await repository.respond(c.get("account").id, { id: c.req.param("inviteID") }, action === "accept");
    return result ? c.json(result) : c.body(null, 204);
  });
  app.use("/space-invitations/:token", async (c, next) => {
    c.header("Cache-Control", "no-store"); c.header("Referrer-Policy", "no-referrer");
    const token = trimSpace(c.req.param("token")); if (!token || Buffer.byteLength(token) > 1024) return c.json({ code: "not_found" }, 404);
    await next();
  });
  app.get("/space-invitations/:token", async (c) => c.json(await repository.preview(trimSpace(c.req.param("token")))));
  app.post("/space-invitations/:token", requireAccount(options.auth), bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }), async (c) => {
    const input = z.object({ accept: z.boolean().nullable().optional().transform((value) => value ?? false) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    const result = await repository.respond(c.get("account").id, { token: trimSpace(c.req.param("token")) }, input.data.accept);
    return result ? c.json(result) : c.body(null, 204);
  });
  return app;
}
