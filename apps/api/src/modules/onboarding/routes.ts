import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { requireAccount, type AccountEnvironment } from "../auth/account-session.js";
import type { AuthService } from "../auth/service.js";
import { InstallationError } from "../official-apps/repository.js";
import type { createOfficialCatalog } from "../official-apps/catalog.js";
import { SpaceError } from "../spaces/model.js";
import type { createOnboardingRepository } from "./repository.js";
export function createOnboardingRoutes(options: { auth: AuthService; catalog: ReturnType<typeof createOfficialCatalog>; repository: ReturnType<typeof createOnboardingRepository> }) {
  const defaults = ["inbox", "chat", "journal", "files", "agents"].map((id) => { const app = options.catalog.find(id); if (!app) throw new Error("Default Misty app is missing from the catalog"); return app; });
  const app = new Hono<AccountEnvironment>();
  app.onError((error, c) => {
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "not_authenticated" ? 401 : error.code === "not_found" ? 404 : error.code === "invalid_request" ? 400 : 409);
    if (error instanceof InstallationError) return c.json({ code: error.code }, error.code === "account_unavailable" ? 401 : 409);
    throw error;
  });
  app.use("/onboarding/finish", requireAccount(options.auth), bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  app.post("/onboarding/finish", async (c) => {
    const input = z.object({ space_name: z.string().nullable().optional().transform((value) => value ?? ""), app_ids: z.array(z.string()).nullable().optional() }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    // Compatibility field app_ids never chooses grants or replaces the reviewed defaults.
    return c.json(await options.repository.finish(c.get("account").id, input.data.space_name, defaults), 201);
  });
  return app;
}
