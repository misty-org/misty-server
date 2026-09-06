import { createHash } from "node:crypto";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { MistyContractError, parseAppRpcRequest, MISTY_MAIL_JSON_MAX_BYTES } from "@misty/contracts";
import { AppSessionRevoked, type AppRuntimeRepository, type AppSession } from "./repository.js";
import { authorizeMethod } from "./authorization.js";
import { createAdmission, type Admission } from "../../../../../packages/runtime/src/admission.js";

type AppEnvironment = { Variables: { appSession: AppSession } };
export type AppRpcRequest = ReturnType<typeof parseAppRpcRequest>;
export interface AppRuntimeDependencies {
  repository: AppRuntimeRepository;
  /** Domain implementations must independently authorize membership and object access. */
  dispatch?: (request: AppRpcRequest, session: AppSession, original: Request) => Promise<Response>;
  largeBodyAdmission?: Admission;
}
const recordBody = z.object({ data: z.json() });
const recordKey = z.string().trim().min(1).refine((value) => Buffer.byteLength(value, "utf8") <= 160);
const ordinaryBodyLimit = 4 * 1024 * 1024;
const draftMethods = new Set(["mail.drafts.create", "mail.drafts.update"]);

export function createAppRuntimeRoutes({ repository, dispatch, largeBodyAdmission = createAdmission(2) }: AppRuntimeDependencies) {
  const app = new Hono<AppEnvironment>();
  app.onError((error, c) => { if (error instanceof AppSessionRevoked) return c.json({ code: "app_session_expired" }, 401); throw error; });
  app.use("*", async (c, next) => {
    const match = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "");
    const token = match?.[1]?.trim();
    if (!token) return c.json({ code: "app_session_required" }, 401);
    const session = await repository.findSession(createHash("sha256").update(token).digest("hex"));
    if (!session || session.expires_at.getTime() <= Date.now()) return c.json({ code: "app_session_expired" }, 401);
    c.set("appSession", session);
    return next();
  });
  app.use("*", async (c, next) => {
    const largeDraftCandidate = c.req.method === "POST" && c.req.path.endsWith("/rpc") && c.get("appSession").scopes.includes("mail.write");
    if (largeDraftCandidate && !await repository.isSpaceMember(c.get("appSession"))) return c.json({ code: "app_runtime_forbidden" }, 403);
    const bounded = () => bodyLimit({
      // Only authenticated mail writers can reach the larger absolute ceiling.
      // The decoded method is checked against the ordinary limit before schemas.
      maxSize: largeDraftCandidate
        ? MISTY_MAIL_JSON_MAX_BYTES + 64 * 1024 : ordinaryBodyLimit,
      onError: (context) => context.json({ code: "invalid_request", message: "Request body is too large." }, 400),
    })(c, next);
    return largeDraftCandidate ? largeBodyAdmission.run(bounded, () => c.json({ code: "mail_capacity_unavailable" }, 503)) : bounded();
  });
  app.get("/session", (c) => {
    const { app_id, scopes, expires_at, space_id } = c.get("appSession");
    return c.json({ app_id, scopes, expires_at, ...(space_id ? { space_id } : {}) });
  });
  app.get("/records", async (c) => {
    const session = c.get("appSession");
    if (!session.scopes.includes("storage.read")) return c.json({ code: "app_scope_forbidden" }, 403);
    return c.json({ records: await repository.listRecords(session) });
  });
  app.put("/records/:recordKey", async (c) => {
    const session = c.get("appSession");
    if (!session.scopes.includes("storage.write")) return c.json({ code: "app_scope_forbidden" }, 403);
    const key = recordKey.safeParse(c.req.param("recordKey"));
    const body = recordBody.safeParse(await c.req.json().catch(() => undefined));
    if (!key.success || !body.success) return c.json({ code: "invalid_app_record" }, 400);
    return c.json(await repository.putRecord(session, key.data, body.data.data));
  });
  app.delete("/records/:recordKey", async (c) => {
    const session = c.get("appSession");
    if (!session.scopes.includes("storage.write")) return c.json({ code: "app_scope_forbidden" }, 403);
    const key = recordKey.safeParse(c.req.param("recordKey"));
    if (!key.success) return c.json({ code: "invalid_app_record" }, 400);
    if (!await repository.deleteRecord(session, key.data)) return c.json({ code: "record_not_found" }, 404);
    return c.body(null, 204);
  });
  if (dispatch) app.post("/rpc", async (c) => {
    const session = c.get("appSession");
    let request: AppRpcRequest;
    try {
      const bytes = await c.req.arrayBuffer(), input: unknown = JSON.parse(Buffer.from(bytes).toString("utf8"));
      const method = input && typeof input === "object" && "method" in input ? input.method : undefined;
      if (bytes.byteLength > ordinaryBodyLimit && (typeof method !== "string" || !draftMethods.has(method))) return c.json({ code: "invalid_request", message: "Request body is too large." }, 400);
      request = parseAppRpcRequest(input, session.space_id);
    } catch (error) {
      if (error instanceof MistyContractError) return c.json({ code: error.code, message: error.message }, 400);
      if (error instanceof SyntaxError) return c.json({ code: "invalid_request", message: "Invalid SDK method request." }, 400);
      throw error;
    }
    if (!authorizeMethod(request.method, session.scopes)) {
      return c.json({ code: "app_scope_forbidden", message: "This App session does not grant that method." }, 403);
    }
    if (!await repository.isSpaceMember(session)) return c.json({ code: "app_runtime_forbidden" }, 403);
    return dispatch(request, session, c.req.raw);
  });
  return app;
}
