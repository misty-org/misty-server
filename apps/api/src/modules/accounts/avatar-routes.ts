import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { createAdmission } from "../../../../../packages/runtime/src/admission.js";
import { hashToken, type AuthService } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AppSessionRevoked, type AppRuntimeRepository } from "../app-runtime/repository.js";
import type { SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import { StorageUnavailable } from "../storage/object-store.js";
import { AccountUnavailable } from "./repository.js";
import { AvatarError, avatarMaxBytes } from "./avatar-png.js";
import type { AvatarService } from "./avatars.js";

export function createAvatarRoutes(options: { auth: AuthService; appRuntime: AppRuntimeRepository; service: AvatarService }) {
  const app = new Hono<{ Variables: { actor: SpaceActor; sessionHash: string } }>(), uploads = createAdmission(2), reads = createAdmission(8);
  app.onError((error, c) => {
    if (error instanceof AccountUnavailable || error instanceof AppSessionRevoked) return c.json({ code: "not_authenticated" }, 401);
    if (error instanceof StorageUnavailable) return c.json({ code: "avatar_storage_unavailable" }, 503);
    if (error instanceof AvatarError) return c.json({ code: error.code }, error.code === "too_large" ? 413 : error.code === "not_found" ? 404 : error.code === "upload_limit" ? 429 : error.code === "upload_expired" ? 409 : 400);
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "not_authenticated" ? 401 : error.code === "forbidden" ? 403 : error.code === "not_found" ? 404 : 400);
    throw error;
  });
  const paths = ["/me/avatar", "/spaces/:spaceID/members/:userID/avatar"];
  for (const path of paths) app.use(path, async (c, next) => {
    c.header("Cache-Control", "no-store");
    const token = sessionToken(c), user = token ? await options.auth.authenticate(token) : null;
    if (user) { c.set("actor", { userId: user.id }); c.set("sessionHash", hashToken(token!)); }
    else {
      if (path === "/me/avatar" || c.req.method !== "GET" && c.req.method !== "HEAD") return c.json({ code: "not_authenticated" }, 401);
      const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
      const session = bearer ? await options.appRuntime.findSession(hashToken(bearer)) : null;
      if (!session) return c.json({ code: "not_authenticated" }, 401);
      if (!session.scopes.includes("spaces.read") || session.space_id !== c.req.param("spaceID")) return c.json({ code: "app_scope_forbidden" }, 403);
      c.set("actor", { userId: session.user_id, appSession: session });
    }
    await next();
  });
  app.use("/me/avatar", async (c, next) => {
    if (c.req.method !== "PUT") return next();
    return uploads.run(next, () => c.json({ code: "avatar_capacity_unavailable" }, 503));
  });
  app.put("/me/avatar", bodyLimit({ maxSize: avatarMaxBytes, onError: (c) => c.json({ code: "too_large" }, 413) }), async (c) =>
    c.json(await options.service.upload({ userId: c.get("actor").userId, sessionHash: c.get("sessionHash") }, Buffer.from(await c.req.arrayBuffer()), c.req.raw.signal)));
  for (const path of paths) app.get(path, async (c) => reads.run(async () => {
    const input = path === "/me/avatar" ? { userId: c.get("actor").userId, sessionHash: c.get("sessionHash") } :
      { actor: c.get("actor"), spaceId: c.req.param("spaceID")!, memberId: c.req.param("userID")!, ...(c.get("sessionHash") ? { sessionHash: c.get("sessionHash") } : {}) };
    const avatar = await options.service.read(input, c.req.raw.signal);
    c.header("Content-Type", "image/png"); c.header("Cache-Control", "private, max-age=300");
    c.header("ETag", `"avatar-${avatar.version}"`); c.header("X-Content-Type-Options", "nosniff");
    return new Response(new Uint8Array(avatar.data), { headers: c.res.headers });
  }, () => c.json({ code: "avatar_capacity_unavailable" }, 503)));
  app.all("/me/avatar", (c) => { c.header("Allow", "GET, PUT"); return c.body(null, 405); });
  return app;
}
