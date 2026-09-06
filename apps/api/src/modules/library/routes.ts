import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { requireAccount, type AccountEnvironment } from "../auth/account-session.js";
import type { AuthService } from "../auth/service.js";
import { AuthBusy } from "../auth/model.js";
import { SpaceError } from "../spaces/model.js";
import { LibraryReauthenticationFailed, LibraryReauthenticationRequired } from "./model.js";
import type { createLibraryRepository } from "./repository.js";
import type { createLibraryReauthentication } from "./reauthentication.js";
import { LibraryObjectMismatch, type createLibraryDownloads } from "./downloads.js";
import { EgressQuotaExceeded } from "../storage/egress.js";
import { StorageUnavailable } from "../storage/object-store.js";
import type { createLibraryMutations } from "./mutations.js";
import type { createLibraryOrganization } from "./organization.js";
import { createOrganizationRoutes } from "./organization-routes.js";
export function createLibraryRoutes(options: { auth: AuthService; repository: ReturnType<typeof createLibraryRepository>; reauthenticate: ReturnType<typeof createLibraryReauthentication>; downloads: ReturnType<typeof createLibraryDownloads>; mutations: ReturnType<typeof createLibraryMutations>; organization: ReturnType<typeof createLibraryOrganization> }) {
  const app = new Hono<AccountEnvironment>(), repository = options.repository;
  app.onError((error, c) => {
    if (error instanceof StorageUnavailable) return c.json({ code: "library_unavailable" }, 503);
    if (error instanceof LibraryObjectMismatch) return c.json({ code: "library_object_mismatch" }, 409);
    if (error instanceof EgressQuotaExceeded) { c.header("Retry-After", "3600"); return c.json({ code: "egress_quota_exceeded", message: "Download limit reached for today. Try again later." }, 429); }
    if (error instanceof LibraryReauthenticationRequired) return c.json({ code: "library_reauthentication_required" }, 401);
    if (error instanceof LibraryReauthenticationFailed) return c.json({ code: "reauthentication_failed" }, 401);
    if (error instanceof AuthBusy) return c.json({ code: "authentication_busy" }, 503);
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "not_authenticated" ? 401 : error.code === "forbidden" ? 403 : error.code === "not_found" ? 404 : error.code === "version_conflict" ? 409 : 400);
    throw error;
  });
  for (const path of ["/spaces/:spaceID/library", "/spaces/:spaceID/library/items/:itemID", "/spaces/:spaceID/library/facets", "/spaces/:spaceID/library/usage", "/spaces/:spaceID/library/reauthenticate", "/spaces/:spaceID/library/items/:itemID/download"]) app.use(path, requireAccount(options.auth));
  const boundedBody = bodyLimit({ maxSize: 1024 * 1024, onError: c => c.json({ code: "invalid_request" }, 400) });
  app.post("/spaces/:spaceID/library/items/bulk", requireAccount(options.auth), boundedBody, async c => c.json(await options.mutations.bulk(c.get("account").id, c.req.param("spaceID"), c.req.header("X-Misty-Library-Reauthentication") ?? "", await c.req.json().catch(() => null))));
  app.patch("/spaces/:spaceID/library/items/:itemID", boundedBody, async c => c.json(await options.mutations.update(c.get("account").id, c.req.param("spaceID"), c.req.param("itemID"), c.req.header("X-Misty-Library-Reauthentication") ?? "", await c.req.json().catch(() => null))));
  for (const action of ["trash", "restore"] as const) app.post(`/spaces/:spaceID/library/items/:itemID/${action}`, requireAccount(options.auth), async c => c.json(await options.mutations.transition(c.get("account").id, c.req.param("spaceID"), c.req.param("itemID"), c.req.header("X-Misty-Library-Reauthentication") ?? "", action === "restore")));
  app.get("/spaces/:spaceID/library/items/:itemID/download", c => options.downloads(c.get("account").id, c.req.param("spaceID"), c.req.param("itemID"), c.req.query("version") === "original", c.req.header("X-Misty-Library-Reauthentication") ?? "", c.req.raw.signal));
  app.get("/spaces/:spaceID/library", async c => c.json(await repository.list(c.get("account").id, c.req.param("spaceID"), c.req.query(), c.req.header("X-Misty-Library-Reauthentication") ?? "")));
  app.get("/spaces/:spaceID/library/items/:itemID", async c => c.json(await repository.get(c.get("account").id, c.req.param("spaceID"), c.req.param("itemID"), c.req.header("X-Misty-Library-Reauthentication") ?? "")));
  app.get("/spaces/:spaceID/library/facets", async c => c.json(await repository.facets(c.get("account").id, c.req.param("spaceID"), c.req.query("q") ?? "")));
  app.get("/spaces/:spaceID/library/usage", async c => c.json(await repository.usage(c.get("account").id, c.req.param("spaceID"))));
  app.post("/spaces/:spaceID/library/reauthenticate", bodyLimit({ maxSize: 8192, onError: c => c.json({ code: "invalid_request" }, 400) }), async c => c.json(await options.reauthenticate(c.get("account").id, c.req.param("spaceID"), await c.req.json().catch(() => null))));
  app.route("/", createOrganizationRoutes(options.auth, options.organization));
  return app;
}
