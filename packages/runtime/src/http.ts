import { randomUUID } from "node:crypto";
import { Hono } from "hono";
import type { Logger } from "./logger.js";

export type HttpEnvironment = { Variables: { requestId: string } };

export function createHttpApp(logger: Logger) {
  const app = new Hono<HttpEnvironment>();
  app.use("*", async (c, next) => {
    // Generate locally: do not let untrusted input forge log correlation IDs.
    const requestId = randomUUID();
    const started = performance.now();
    c.set("requestId", requestId);
    c.header("X-Request-ID", requestId);
    c.header("X-Content-Type-Options", "nosniff");
    await next();
    // Request URLs, bodies and headers may contain account or integration secrets.
    logger.info({ requestId, method: c.req.method, status: c.res.status, durationMs: Math.round(performance.now() - started) }, "request completed");
  });
  app.onError((error, c) => {
    // Do not serialize arbitrary exception messages: driver errors may contain SQL or credentials.
    logger.error({ requestId: c.get("requestId"), errorType: error.name }, "request failed");
    return c.json({ code: "internal_error", message: "An internal error occurred." }, 500);
  });
  app.notFound((c) => c.json({ code: "not_found", message: "Route not found." }, 404));
  return app;
}
