import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import type { AuthService } from "../auth/service.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AppSessionRevoked, type AppRuntimeRepository } from "../app-runtime/repository.js";
import type { SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import type { createRoadmapRepository } from "./roadmaps/repository.js";
import type { createTaskRepository } from "./tasks/repository.js";
import type { createCalendarRepository } from "./calendar/repository.js";

import type { createCalendarSourceService } from "./calendar/source-service.js";
import { CalendarProviderError } from "./calendar/source-model.js";

export const plannerRpcMethods = new Set(["tasks.list", "tasks.activity.list", "tasks.create", "tasks.update", "tasks.delete", "tasks.move",
  "calendar.events.list", "calendar.events.create", "calendar.events.update", "calendar.events.delete", "agenda.list", "calendar.sources.list", "calendar.sources.create", "calendar.sources.delete", "calendar.google.calendars", "calendar.sync", "roadmaps.list", "roadmaps.create", "roadmaps.get", "roadmaps.update", "roadmaps.delete", "roadmaps.milestones.create", "roadmaps.milestones.update", "roadmaps.milestones.delete",
  "roadmaps.goals.create", "roadmaps.goals.update", "roadmaps.goals.delete", "roadmaps.goals.setTasks",
  "roadmaps.nodeDefinitions.list", "roadmaps.nodeDefinitions.create", "roadmaps.nodeDefinitions.update", "roadmaps.nodeDefinitions.delete", "roadmaps.nodes.create", "roadmaps.nodes.update", "roadmaps.nodes.delete",
  "roadmaps.edges.create", "roadmaps.edges.update", "roadmaps.edges.delete", "roadmaps.layout.update"]);
export function createPlannerRoutes(options: { auth: AuthService; appRuntime: AppRuntimeRepository; tasks: ReturnType<typeof createTaskRepository>;
  calendar?: ReturnType<typeof createCalendarRepository>; calendarSources?: ReturnType<typeof createCalendarSourceService>; roadmaps?: ReturnType<typeof createRoadmapRepository> }) {
  const app = new Hono<{ Variables: { actor: SpaceActor } }>();
  app.onError((error, c) => {
    if (error instanceof CalendarProviderError) return c.json({ code: error.code }, error.httpStatus);
    if (error instanceof AppSessionRevoked) return c.json({ code: "app_session_expired" }, 401);
    if (error instanceof SpaceError) return c.json({ code: error.code }, error.code === "not_authenticated" ? 401 : error.code === "not_found" ? 404 : error.code === "forbidden" ? 403 : error.code === "version_conflict" ? 409 : 400);
    throw error;
  });
  for (const path of ["/spaces/:spaceID/tasks", "/spaces/:spaceID/tasks/:taskID", "/spaces/:spaceID/tasks/:taskID/activity", "/spaces/:spaceID/tasks/:taskID/move",
    "/spaces/:spaceID/calendar/events", "/spaces/:spaceID/calendar/events/:eventID", "/spaces/:spaceID/agenda", "/spaces/:spaceID/calendar/sources", "/spaces/:spaceID/calendar/sources/:sourceID",
    "/spaces/:spaceID/calendar/google/calendars", "/spaces/:spaceID/calendar/sync", "/spaces/:spaceID/roadmaps", "/spaces/:spaceID/roadmaps/:roadmapID", "/spaces/:spaceID/roadmaps/:roadmapID/milestones", "/spaces/:spaceID/roadmaps/:roadmapID/milestones/:milestoneID",
    "/spaces/:spaceID/roadmaps/:roadmapID/goals", "/spaces/:spaceID/roadmaps/:roadmapID/goals/:goalID", "/spaces/:spaceID/roadmaps/:roadmapID/goals/:goalID/tasks",
    "/spaces/:spaceID/roadmap-node-definitions", "/spaces/:spaceID/roadmap-node-definitions/:definitionID", "/spaces/:spaceID/roadmaps/:roadmapID/nodes", "/spaces/:spaceID/roadmaps/:roadmapID/nodes/:nodeID",
    "/spaces/:spaceID/roadmaps/:roadmapID/edges", "/spaces/:spaceID/roadmaps/:roadmapID/edges/:edgeID", "/spaces/:spaceID/roadmaps/:roadmapID/layout"]) app.use(path, async (c, next) => {
    c.header("Cache-Control", "no-store");
    const token = sessionToken(c), account = token ? await options.auth.authenticate(token) : null;
    if (account) c.set("actor", { userId: account.id });
    else {
      const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
      const session = bearer ? await options.appRuntime.findSession(hashToken(bearer)) : null;
      if (!session) return c.json({ code: "not_authenticated" }, 401);
      const namespace = path.includes("/calendar/") ? "calendar" : path.includes("/roadmap") ? "roadmaps" : "tasks";
      if (session.space_id !== c.req.param("spaceID") || !session.scopes.includes(`${namespace}.${c.req.method === "GET" ? "read" : "write"}`)) return c.json({ code: "app_scope_forbidden" }, 403);
      if (path.endsWith("/calendar/sync") && !session.scopes.includes("tasks.write") || path.endsWith("/calendar/google/calendars") && !session.scopes.includes("connections.read")) return c.json({ code: "app_scope_forbidden" }, 403);
      c.set("actor", { userId: session.user_id, appSession: session });
    }
    await next();
  });
  app.get("/spaces/:spaceID/tasks", async (c) => c.json(await options.tasks.list(c.get("actor"), c.req.param("spaceID"), c.req.query())));
  app.get("/spaces/:spaceID/tasks/:taskID/activity", async (c) => c.json(await options.tasks.activity(c.get("actor"), c.req.param("spaceID"), c.req.param("taskID"))));
  const limit = bodyLimit({ maxSize: 4 * 1024 * 1024, onError: c => c.json({ code: "invalid_request" }, 400) });
  app.post("/spaces/:spaceID/tasks", limit, async c => c.json(await options.tasks.create(c.get("actor"), c.req.param("spaceID"), await c.req.json().catch(() => null)), 201));
  app.patch("/spaces/:spaceID/tasks/:taskID", limit, async c => c.json(await options.tasks.update(c.get("actor"), c.req.param("spaceID"), c.req.param("taskID"), await c.req.json().catch(() => null))));
  app.delete("/spaces/:spaceID/tasks/:taskID", async c => c.json(await options.tasks.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("taskID"), c.req.query("version") ?? "")));
  app.post("/spaces/:spaceID/tasks/:taskID/move", limit, async c => c.json(await options.tasks.move(c.get("actor"), c.req.param("spaceID"), c.req.param("taskID"), await c.req.json().catch(() => null))));
  if (options.calendar) {
    const calendar = options.calendar;
    app.get("/spaces/:spaceID/calendar/events", async c => c.json(await calendar.list(c.get("actor"), c.req.param("spaceID"), c.req.query())));
    app.get("/spaces/:spaceID/agenda", async c => c.json(await calendar.agenda(c.get("actor"), c.req.param("spaceID"), c.req.query())));
    app.post("/spaces/:spaceID/calendar/events", limit, async c => c.json(await calendar.create(c.get("actor"), c.req.param("spaceID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/calendar/events/:eventID", limit, async c => c.json(await calendar.update(c.get("actor"), c.req.param("spaceID"), c.req.param("eventID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/calendar/events/:eventID", async c => {
      await calendar.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("eventID"), c.req.query("version") ?? ""); return c.body(null, 204);
    });
  }
  if (options.calendarSources) {
    const sources = options.calendarSources;
    app.get("/spaces/:spaceID/calendar/sources", async c => c.json(await sources.list(c.get("actor"), c.req.param("spaceID"))));
    app.get("/spaces/:spaceID/calendar/google/calendars", async c => c.json(await sources.available(c.get("actor"), c.req.param("spaceID"), c.req.query("integration_id") ?? "", c.req.raw.signal)));
    app.post("/spaces/:spaceID/calendar/sources", limit, async c => c.json(await sources.create(c.get("actor"), c.req.param("spaceID"), await c.req.json().catch(() => null), c.req.raw.signal), 201));
    app.delete("/spaces/:spaceID/calendar/sources/:sourceID", async c => { await sources.disable(c.get("actor"), c.req.param("spaceID"), c.req.param("sourceID")); return c.body(null, 204); });
    app.post("/spaces/:spaceID/calendar/sync", limit, async c => c.json(await sources.sync(c.get("actor"), c.req.param("spaceID"), await c.req.json().catch(() => null), c.req.raw.signal)));
  }
  if (options.roadmaps) {
    const roadmaps = options.roadmaps;
    app.post("/spaces/:spaceID/roadmaps/:roadmapID/edges", limit, async c => c.json(await roadmaps.edges.create(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/roadmaps/:roadmapID/edges/:edgeID", limit, async c => c.json(await roadmaps.edges.update(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("edgeID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/roadmaps/:roadmapID/edges/:edgeID", async c => c.json(await roadmaps.edges.delete(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("edgeID"), c.req.query("expected_version") ?? "")));
    app.patch("/spaces/:spaceID/roadmaps/:roadmapID/layout", limit, async c => c.json(await roadmaps.layout.update(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), await c.req.json().catch(() => null))));
    app.get("/spaces/:spaceID/roadmap-node-definitions", async c => c.json(await roadmaps.definitions.list(c.get("actor"), c.req.param("spaceID"))));
    app.post("/spaces/:spaceID/roadmap-node-definitions", limit, async c => c.json(await roadmaps.definitions.create(c.get("actor"), c.req.param("spaceID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/roadmap-node-definitions/:definitionID", limit, async c => c.json(await roadmaps.definitions.update(c.get("actor"), c.req.param("spaceID"), c.req.param("definitionID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/roadmap-node-definitions/:definitionID", async c => { await roadmaps.definitions.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("definitionID"), c.req.query("expected_version") ?? ""); return c.body(null, 204); });
    app.post("/spaces/:spaceID/roadmaps/:roadmapID/nodes", limit, async c => c.json(await roadmaps.nodes.create(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/roadmaps/:roadmapID/nodes/:nodeID", limit, async c => c.json(await roadmaps.nodes.update(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("nodeID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/roadmaps/:roadmapID/nodes/:nodeID", async c => c.json(await roadmaps.nodes.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("nodeID"), c.req.query("expected_version") ?? "")));
    app.post("/spaces/:spaceID/roadmaps/:roadmapID/milestones", limit, async c => c.json(await roadmaps.milestones.create(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/roadmaps/:roadmapID/milestones/:milestoneID", limit, async c => c.json(await roadmaps.milestones.update(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("milestoneID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/roadmaps/:roadmapID/milestones/:milestoneID", async c => c.json(await roadmaps.milestones.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("milestoneID"), c.req.query("expected_version") ?? "")));
    app.post("/spaces/:spaceID/roadmaps/:roadmapID/goals", limit, async c => c.json(await roadmaps.goals.create(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/roadmaps/:roadmapID/goals/:goalID", limit, async c => c.json(await roadmaps.goals.update(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("goalID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/roadmaps/:roadmapID/goals/:goalID", async c => c.json(await roadmaps.goals.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("goalID"), c.req.query("expected_version") ?? "")));
    app.put("/spaces/:spaceID/roadmaps/:roadmapID/goals/:goalID/tasks", limit, async c => c.json(await roadmaps.goals.setTasks(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.param("goalID"), await c.req.json().catch(() => null))));
    app.get("/spaces/:spaceID/roadmaps", async c => c.json(await roadmaps.list(c.get("actor"), c.req.param("spaceID"))));
    app.get("/spaces/:spaceID/roadmaps/:roadmapID", async c => c.json(await roadmaps.get(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"))));
    app.post("/spaces/:spaceID/roadmaps", limit, async c => c.json(await roadmaps.create(c.get("actor"), c.req.param("spaceID"), await c.req.json().catch(() => null)), 201));
    app.patch("/spaces/:spaceID/roadmaps/:roadmapID", limit, async c => c.json(await roadmaps.update(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), await c.req.json().catch(() => null))));
    app.delete("/spaces/:spaceID/roadmaps/:roadmapID", async c => c.json(await roadmaps.archive(c.get("actor"), c.req.param("spaceID"), c.req.param("roadmapID"), c.req.query("expected_version") ?? "")));
  }
  return app;
}
