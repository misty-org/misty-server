import { Hono } from "hono";
import type { createCalendarSourceJobs } from "./source-jobs.js";
export function createCalendarCallbackRoutes(jobs: ReturnType<typeof createCalendarSourceJobs>) {
  const app = new Hono();
  app.post("/provider-callbacks/google/calendar", async c => {
    c.header("Cache-Control", "no-store");
    const result = await jobs.callback(c.req.header("X-Goog-Channel-ID") ?? "", c.req.header("X-Goog-Channel-Token") ?? "", c.req.header("X-Goog-Resource-ID") ?? "");
    return c.body(null, result === "accepted" ? 204 : result === "invalid" ? 400 : 404);
  });
  return app;
}
