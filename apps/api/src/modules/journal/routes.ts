import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import type { AuthService } from "../auth/service.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AppSessionRevoked, type AppRuntimeRepository } from "../app-runtime/repository.js";
import type { AppRuntimeDependencies } from "../app-runtime/routes.js";
import { createNativeDispatcher } from "../app-runtime/dispatch.js";
import type { createJournalNotes } from "./notes.js";
import type { createJournalDrawings } from "./drawings.js";
import { JournalError, type JournalActor } from "./access.js";
import { assetInput, drawingAssetInput, type createJournalAssets } from "./assets.js";
import { AssetError } from "./asset-repository.js";
import { StorageUnavailable } from "../storage/object-store.js";
import { StorageQuotaExceeded } from "../storage/quota.js";

export type JournalDependencies = { auth: AuthService; appRuntime: AppRuntimeRepository; notes: ReturnType<typeof createJournalNotes>; drawings: ReturnType<typeof createJournalDrawings>; assets?: ReturnType<typeof createJournalAssets> };
const title = z.object({ title: z.string().nullable().optional().transform((value) => value ?? "") }).strict();
const metadata = z.object({ shared_tags: z.array(z.string()).nullable().optional().transform((value) => value ?? []) }).strict();
const archive = z.object({ archived: z.boolean() }).strict();
export const journalRpcMethods = new Set(["notes.list", "notes.get", "notes.create", "notes.update", "notes.archive", "notes.delete", "notes.backlinks", "notes.collaboration.ticket",
  "drawings.list", "drawings.get", "drawings.create", "drawings.update", "drawings.delete", "drawings.collaboration.ticket",
  "notes.assets.reserve", "notes.assets.finalize", "notes.assets.download", "drawings.assets.reserve", "drawings.assets.finalize", "drawings.assets.download"]);

export function createJournalRoutes(options: JournalDependencies) {
  const app = new Hono<{ Variables: { actor: JournalActor } }>(), { notes, drawings } = options;
  app.onError((error, c) => {
    if (error instanceof StorageUnavailable) return c.json({ code: "storage_unavailable" }, 503);
    if (error instanceof StorageQuotaExceeded) return c.json({ code: "owner_storage_quota_exceeded", reason: `${error.dimension}_storage_limit_reached`, owner_can_upgrade: true }, 409);
    if (error instanceof AssetError) return c.json({ code: error.code === "upload_mismatch" ? "upload_verification_failed" : error.code === "conflict" ? "version_conflict" : error.code }, error.code === "forbidden" ? 403 : error.code === "upload_mismatch" ? 422 : 409);
    if (error instanceof AppSessionRevoked) return c.json({ code: "app_session_expired" }, 401);
    if (error instanceof JournalError) {
      if (error.code === "space_forbidden") return c.json({ code: "forbidden" }, 403);
      return c.json({ code: error.code }, error.code === "not_found" ? 404 : error.code === "collaboration_unavailable" ? 503 : 400);
    }
    throw error;
  });
  for (const domain of ["notes", "drawings"] as const) for (const suffix of ["", "/*"]) {
    app.use(`/spaces/:spaceID/${domain}${suffix}`, async (c, next) => {
      c.header("Cache-Control", "no-store");
      const token = sessionToken(c), account = token ? await options.auth.authenticate(token) : null;
      if (account) c.set("actor", { userId: account.id });
      else {
        const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
        const session = bearer ? await options.appRuntime.findSession(hashToken(bearer)) : null;
        if (!session) return c.json({ code: "not_authenticated" }, 401);
        const scope = `${domain}.${c.req.method === "GET" || c.req.method === "HEAD" ? "read" : "write"}`;
        if (session.space_id !== c.req.param("spaceID") || !session.scopes.includes(scope)) return c.json({ code: "app_scope_forbidden" }, 403);
        c.set("actor", { userId: session.user_id, appSession: session });
      }
      await next();
    });
    app.use(`/spaces/:spaceID/${domain}${suffix}`, bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  app.get("/spaces/:spaceID/notes", async (c) => c.json({ notes: await notes.list(c.get("actor"), c.req.param("spaceID")) }));
  app.post("/spaces/:spaceID/notes", async (c) => {
    const input = title.safeParse(await c.req.json().catch(() => undefined)); if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await notes.create(c.get("actor"), c.req.param("spaceID"), input.data.title), 201);
  });
  app.get("/spaces/:spaceID/notes/:noteID", async (c) => c.json(await notes.get(c.get("actor"), c.req.param("spaceID"), c.req.param("noteID"))));
  app.patch("/spaces/:spaceID/notes/:noteID/metadata", async (c) => {
    const input = metadata.safeParse(await c.req.json().catch(() => undefined)); if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await notes.metadata(c.get("actor"), c.req.param("spaceID"), c.req.param("noteID"), input.data.shared_tags));
  });
  app.patch("/spaces/:spaceID/notes/:noteID", async (c) => {
    const input = archive.safeParse(await c.req.json().catch(() => undefined)); if (!input.success) return c.json({ code: "invalid_request" }, 400);
    await notes.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("noteID"), input.data.archived); return c.body(null, 204);
  });
  app.delete("/spaces/:spaceID/notes/:noteID", async (c) => { await notes.delete(c.get("actor"), c.req.param("spaceID"), c.req.param("noteID")); return c.body(null, 204); });
  app.get("/spaces/:spaceID/notes/:noteID/backlinks", async (c) => c.json({ backlinks: await notes.backlinks(c.get("actor"), c.req.param("spaceID"), c.req.param("noteID")) }));
  app.post("/spaces/:spaceID/notes/:noteID/collaboration-ticket", async (c) => {
    c.header("Cache-Control", "private, no-store"); return c.json(await notes.ticket(c.get("actor"), c.req.param("spaceID"), c.req.param("noteID")), 201);
  });
  app.get("/spaces/:spaceID/drawings", async (c) => c.json({ drawings: await drawings.list(c.get("actor"), c.req.param("spaceID")) }));
  app.post("/spaces/:spaceID/drawings", async (c) => {
    const input = title.safeParse(await c.req.json().catch(() => undefined)); if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await drawings.create(c.get("actor"), c.req.param("spaceID"), input.data.title), 201);
  });
  app.get("/spaces/:spaceID/drawings/:drawingID", async (c) => c.json(await drawings.get(c.get("actor"), c.req.param("spaceID"), c.req.param("drawingID"))));
  app.patch("/spaces/:spaceID/drawings/:drawingID", async (c) => {
    const input = title.safeParse(await c.req.json().catch(() => undefined)); if (!input.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await drawings.rename(c.get("actor"), c.req.param("spaceID"), c.req.param("drawingID"), input.data.title));
  });
  app.delete("/spaces/:spaceID/drawings/:drawingID", async (c) => { await drawings.delete(c.get("actor"), c.req.param("spaceID"), c.req.param("drawingID")); return c.body(null, 204); });
  app.post("/spaces/:spaceID/drawings/:drawingID/collaboration-ticket", async (c) => {
    c.header("Cache-Control", "private, no-store"); return c.json(await drawings.ticket(c.get("actor"), c.req.param("spaceID"), c.req.param("drawingID")), 201);
  });
  for (const kind of ["note", "drawing"] as const) {
    const base = `/spaces/:spaceID/${kind}s/:resourceID/assets` as const;
    app.get(base, async (c) => {
      if (!options.assets) return c.json({ code: "method_not_migrated" }, 501);
      return c.json({ assets: await options.assets.list(c.get("actor"), c.req.param("spaceID"), kind, c.req.param("resourceID")) });
    });
    app.delete(`${base}/:assetID`, async (c) => {
      if (!options.assets) return c.json({ code: "method_not_migrated" }, 501);
      await options.assets.remove(c.get("actor"), c.req.param("spaceID"), kind, c.req.param("resourceID"), c.req.param("assetID"));
      return c.body(null, 204);
    });
    app.post(`${base}/uploads`, async (c) => {
      if (!options.assets) return c.json({ code: "method_not_migrated" }, 501);
      const input = (kind === "note" ? assetInput : drawingAssetInput).safeParse(await c.req.json().catch(() => undefined));
      if (!input.success) return c.json({ code: "invalid_request" }, 400);
      return c.json(await options.assets.reserve(c.get("actor"), c.req.param("spaceID"), kind, c.req.param("resourceID"), input.data), 201);
    });
    app.post(`${base}/uploads/:uploadID/finalize`, async (c) => {
      if (!options.assets) return c.json({ code: "method_not_migrated" }, 501);
      return c.json(await options.assets.finalize(c.get("actor"), c.req.param("spaceID"), kind, c.req.param("resourceID"),
        c.req.param("uploadID"), c.req.header("X-Misty-Library-Upload-Token")?.trim() ?? "", c.req.raw.signal));
    });
    app.get(`${base}/:assetID/download`, async (c) => {
      if (!options.assets) return c.json({ code: "method_not_migrated" }, 501);
      return c.json(await options.assets.download(c.get("actor"), c.req.param("spaceID"), kind, c.req.param("resourceID"), c.req.param("assetID")));
    });
  }
  return app;
}

/** Reuse native HTTP validation/authorization; never retry a mutation against Go. */
export function createJournalDispatcher(routes: ReturnType<typeof createJournalRoutes>): NonNullable<AppRuntimeDependencies["dispatch"]> {
  return createNativeDispatcher([{ methods: journalRpcMethods, request: (request) => routes.request(request) }]);
}
