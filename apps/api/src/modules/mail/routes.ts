import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { MailThreadActionSchema, MailDraftInputSchema, IdentifierSchema, MISTY_MAIL_JSON_MAX_BYTES } from "@misty/contracts";
import { z } from "zod";
import type { AuthService } from "../auth/service.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AppSessionRevoked, type AppRuntimeRepository } from "../app-runtime/repository.js";
import type { SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import { ConnectionTokenError } from "../connections/token-broker.js";
import type { createMailService } from "./service.js";
import { MailError, mailError, mailStatus } from "./errors.js";
import { createAdmission, type Admission } from "../../../../../packages/runtime/src/admission.js";
import { nativeBody } from "../app-runtime/native-body.js";

export const mailRpcMethods = new Set(["mail.accounts.list", "mail.folders.list", "mail.threads.list", "mail.threads.get", "mail.threads.action", "mail.drafts.create", "mail.drafts.update", "mail.drafts.send"]);
export function createMailRoutes(options: { auth: AuthService; appRuntime: AppRuntimeRepository; service: ReturnType<typeof createMailService>; draftAdmission?: Admission }) {
  const app = new Hono<{ Variables: { actor: SpaceActor } }>();
  const admission = options.draftAdmission ?? createAdmission(2);
  app.onError((error, c) => {
    if (error instanceof AppSessionRevoked) return c.json({ code: "app_session_expired" }, 401);
    if (error instanceof SpaceError && error.code === "not_authenticated") return c.json({ code: "not_authenticated" }, 401);
    if (error instanceof SpaceError && error.code === "forbidden") return c.json({ code: "forbidden" }, 403);
    if (error instanceof MailError || error instanceof ConnectionTokenError || error instanceof SpaceError) {
      const mapped = mailError(error); return c.json({ code: mapped.code }, mailStatus(mapped));
    }
    throw error;
  });
  for (const path of ["/mail/accounts", "/mail/folders", "/mail/threads", "/mail/threads/:threadID", "/mail/threads/:threadID/actions", "/mail/drafts", "/mail/drafts/:draftID", "/mail/drafts/:draftID/send"]) app.use(path, async (c, next) => {
    c.header("Cache-Control", "no-store");
    const token = sessionToken(c), account = token ? await options.auth.authenticate(token) : null;
    if (account) c.set("actor", { userId: account.id });
    else {
      const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
      const session = bearer ? await options.appRuntime.findSession(hashToken(bearer)) : null;
      if (!session) return c.json({ code: "not_authenticated" }, 401);
      if (!session.scopes.includes(c.req.method === "GET" ? "mail.read" : "mail.write")) return c.json({ code: "app_scope_forbidden" }, 403);
      c.set("actor", { userId: session.user_id, appSession: session });
    }
    await next();
  });
  for (const path of ["/mail/drafts", "/mail/drafts/:draftID"]) app.use(path, async (c, next) => admission.run(() => next(), () => c.json({ code: "mail_capacity_unavailable" }, 503)));
  const draftLimit = bodyLimit({ maxSize: MISTY_MAIL_JSON_MAX_BYTES, onError: (c) => c.json({ code: "mail_body_too_large" }, 413) });
  app.post("/mail/drafts", draftLimit, async (c) => {
    const input = MailDraftInputSchema.safeParse(nativeBody(c.req.raw, "mail.drafts.create") ?? await c.req.json().catch(() => null)); if (!input.success) throw new MailError("mail_invalid_request");
    return c.json(await options.service.createDraft(c.get("actor"), input.data, c.req.raw.signal), 201);
  });
  app.put("/mail/drafts/:draftID", draftLimit, async (c) => {
    const id = c.req.param("draftID").trim(), input = MailDraftInputSchema.safeParse(nativeBody(c.req.raw, "mail.drafts.update") ?? await c.req.json().catch(() => null));
    if (!/^[\x21-\x7e]{1,320}$/.test(id) || id === "." || id === ".." || !input.success) throw new MailError("mail_invalid_request");
    return c.json(await options.service.updateDraft(c.get("actor"), id, input.data, c.req.raw.signal));
  });
  app.post("/mail/drafts/:draftID/send", bodyLimit({ maxSize: 16384, onError: (c) => c.json({ code: "mail_invalid_request" }, 413) }), async (c) => {
    const id = c.req.param("draftID").trim(), input = z.strictObject({ connection_id: IdentifierSchema,
      authoring_source: z.string().trim().toLowerCase().pipe(z.enum(["user", "ai"])), confirmed: z.boolean().default(false) }).safeParse(await c.req.json().catch(() => null));
    if (!/^[\x21-\x7e]{1,320}$/.test(id) || id === "." || id === ".." || !input.success) throw new MailError("mail_invalid_request");
    return c.json(await options.service.sendDraft(c.get("actor"), id, input.data, c.req.raw.signal));
  });
  app.get("/mail/accounts", async (c) => c.json(await options.service.accounts(c.get("actor"), c.req.raw.signal)));
  app.get("/mail/folders", async (c) => c.json(await options.service.folders(c.get("actor"), c.req.query("connection_id") ?? "", c.req.raw.signal)));
  app.get("/mail/threads", async (c) => {
    const raw = c.req.query(), page = raw.page_size?.trim() ?? "", pageSize = page ? Number(page) : 50;
    const query = { pageSize, pageToken: raw.page_token?.trim() ?? "", query: raw.query?.trim() ?? "", folderId: raw.folder_id?.trim() ?? "" };
    if (page && !/^[+-]?\d+$/.test(page) || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100 || Buffer.byteLength(query.query) > 2000 || Buffer.byteLength(query.pageToken) > 4096 || Buffer.byteLength(query.folderId) > 320) throw new MailError("mail_invalid_request");
    return c.json(await options.service.threads(c.get("actor"), raw.connection_id ?? "", query, c.req.raw.signal));
  });
  app.get("/mail/threads/:threadID", async (c) => {
    const id = c.req.param("threadID").trim();
    if (!/^[\x21-\x7e]{1,320}$/.test(id) || id === "." || id === "..") throw new MailError("mail_invalid_request");
    return c.json(await options.service.thread(c.get("actor"), c.req.query("connection_id") ?? "", id, c.req.raw.signal));
  });
  app.post("/mail/threads/:threadID/actions", bodyLimit({ maxSize: 16384, onError: (c) => c.json({ code: "mail_invalid_request" }, 413) }), async (c) => {
    const id = c.req.param("threadID").trim(), input = MailThreadActionSchema.safeParse(await c.req.json().catch(() => null));
    if (!/^[\x21-\x7e]{1,320}$/.test(id) || id === "." || id === ".." || !input.success) throw new MailError("mail_invalid_request");
    const { connection_id, ...changes } = input.data;
    return c.json(await options.service.modifyThread(c.get("actor"), connection_id, id, changes, c.req.raw.signal));
  });
  return app;
}
