import { createHttpApp } from "../../../packages/runtime/src/http.js";
import type { Logger } from "../../../packages/runtime/src/logger.js";
import { createAppRuntimeRoutes } from "./modules/app-runtime/routes.js";
import type { AppRuntimeDependencies } from "./modules/app-runtime/routes.js";
import { createEntitlementRoutes } from "./modules/entitlements/routes.js";
import { createAuthRoutes } from "./modules/auth/routes.js";
import { createSelfHostRoutes } from "./modules/self-host/routes.js";
import { createSelfHostGate } from "./modules/self-host/gate.js";
import { hashToken } from "./modules/auth/service.js";
import { createSelfHostIssuanceRoutes } from "./modules/self-host/issuance-routes.js";
import { createBillingRoutes } from "./modules/billing/routes.js";
import { createAccountRoutes } from "./modules/accounts/routes.js";
import { createAccountDeletionRoutes } from "./modules/accounts/deletion/routes.js";
import { createAvatarRoutes } from "./modules/accounts/avatar-routes.js";
import { createOfficialAppRoutes } from "./modules/official-apps/routes.js";
import { createJournalRoutes, journalRpcMethods, type JournalDependencies } from "./modules/journal/routes.js";
import { createInvitationRoutes } from "./modules/spaces/invitations/routes.js";
import { createSpaceRoutes } from "./modules/spaces/routes.js";
import { createCalendarCallbackRoutes } from "./modules/planner/calendar/callback-routes.js";
import { createPlannerRoutes, plannerRpcMethods } from "./modules/planner/routes.js";
import { createConnectionRoutes, connectionRpcMethods } from "./modules/connections/routes.js";
import { createMailRoutes, mailRpcMethods } from "./modules/mail/routes.js";
import { createOnboardingRoutes } from "./modules/onboarding/routes.js";
import { createNativeDispatcher } from "./modules/app-runtime/dispatch.js";
import { createNoteProjectionRoutes } from "./modules/collaboration/projections.js";
import { createAdmission } from "../../../packages/runtime/src/admission.js";
import { createRuntimeCallbackRoutes } from "./modules/misty/runtime-routes.js";
import { createLibraryRoutes } from "./modules/library/routes.js";

export interface ApiDependencies {
  logger: Logger;
  checkDatabase: () => Promise<void>;
  isDraining: () => boolean;
  migrationComplete: boolean;
  appRuntime?: AppRuntimeDependencies;
  entitlements?: Parameters<typeof createEntitlementRoutes>[0];
  auth?: Parameters<typeof createAuthRoutes>[0];
  selfHost?: Parameters<typeof createSelfHostRoutes>[0];
  selfHostIssuance?: Parameters<typeof createSelfHostIssuanceRoutes>[0];
  accounts?: Parameters<typeof createAccountRoutes>[0];
  accountDeletion?: Parameters<typeof createAccountDeletionRoutes>[0];
  billing?: Parameters<typeof createBillingRoutes>[0];
  avatars?: Parameters<typeof createAvatarRoutes>[0];
  officialApps?: Parameters<typeof createOfficialAppRoutes>[0];
  invitations?: Parameters<typeof createInvitationRoutes>[0];
  spaces?: Parameters<typeof createSpaceRoutes>[0];
  planner?: Parameters<typeof createPlannerRoutes>[0];
  calendarCallbacks?: Parameters<typeof createCalendarCallbackRoutes>[0];
  connections?: Parameters<typeof createConnectionRoutes>[0];
  mail?: Parameters<typeof createMailRoutes>[0];
  onboarding?: Parameters<typeof createOnboardingRoutes>[0];
  journal?: JournalDependencies;
  noteProjections?: Parameters<typeof createNoteProjectionRoutes>[0];
  runtimeCallbacks?: Parameters<typeof createRuntimeCallbackRoutes>[0];
  library?: Parameters<typeof createLibraryRoutes>[0];
}

/** No sockets, environment reads, background work or global database state. */
export function createApi(dependencies: ApiDependencies) {
  const app = createHttpApp(dependencies.logger);
  const draftAdmission = createAdmission(2);
  const journal = dependencies.journal ? createJournalRoutes(dependencies.journal) : undefined;
  const spaces = dependencies.spaces ? createSpaceRoutes(dependencies.spaces) : undefined;
  const planner = dependencies.planner ? createPlannerRoutes(dependencies.planner) : undefined;
  const connections = dependencies.connections ? createConnectionRoutes(dependencies.connections) : undefined;
  const mail = dependencies.mail ? createMailRoutes({ ...dependencies.mail, draftAdmission }) : undefined;
  if (dependencies.auth) {
    app.use("*", dependencies.auth.boundary.middleware);
    if (dependencies.auth.deployment === "self_hosted") app.use("*", createSelfHostGate({
      owner: async (token) => (await dependencies.auth!.service.authenticate(token))?.id ??
        (await dependencies.appRuntime?.repository.findSession(hashToken(token)))?.user_id ?? null,
      access: async (userId) => dependencies.selfHost ? dependencies.selfHost.service.access(userId) : null,
      now: () => new Date(dependencies.auth!.now?.() ?? Date.now()),
    }));
    const auth = createAuthRoutes(dependencies.auth);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", auth);
  }
  if (dependencies.selfHost) {
    const routes = createSelfHostRoutes(dependencies.selfHost);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.selfHostIssuance && dependencies.auth?.deployment === "hosted") {
    const routes = createSelfHostIssuanceRoutes(dependencies.selfHostIssuance);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.entitlements) app.route("/internal/payments/entitlements", createEntitlementRoutes(dependencies.entitlements));
  if (dependencies.runtimeCallbacks) app.route("/", createRuntimeCallbackRoutes(dependencies.runtimeCallbacks));
  if (dependencies.accounts) {
    const routes = createAccountRoutes(dependencies.accounts);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.library) {
    const routes = createLibraryRoutes(dependencies.library);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.accountDeletion) {
    const routes = createAccountDeletionRoutes(dependencies.accountDeletion);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.billing) {
    const routes = createBillingRoutes(dependencies.billing);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.avatars) {
    const routes = createAvatarRoutes(dependencies.avatars);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.officialApps) {
    const routes = createOfficialAppRoutes(dependencies.officialApps);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.appRuntime) {
    for (const prefix of ["", "/api", "/v1"]) {
      app.route(`${prefix}/app-runtime`, createAppRuntimeRoutes({ ...dependencies.appRuntime, largeBodyAdmission: draftAdmission,
        ...(journal || spaces || planner || connections || mail ? { dispatch: createNativeDispatcher([
          ...(journal ? [{ methods: journalRpcMethods, request: (request: Request) => journal.request(request) }] : []),
          ...(spaces ? [{ methods: new Set(["spaces.get", "spaces.members.list"]), request: (request: Request) => spaces.request(request) }] : []),
          ...(planner ? [{ methods: plannerRpcMethods, request: (request: Request) => planner.request(request) }] : []),
          ...(connections ? [{ methods: connectionRpcMethods, request: (request: Request) => connections.request(request) }] : []),
          ...(mail ? [{ methods: mailRpcMethods, validatedBodies: new Set(["mail.drafts.create", "mail.drafts.update"]), request: (request: Request) => mail.request(request) }] : []),
        ]) } : {}) }));
    }
  }
  if (spaces) for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", spaces);
  if (dependencies.calendarCallbacks) for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", createCalendarCallbackRoutes(dependencies.calendarCallbacks));
  if (planner) for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", planner);
  if (connections) for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", connections);
  if (mail) for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", mail);
  if (dependencies.invitations) {
    const routes = createInvitationRoutes(dependencies.invitations);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (dependencies.onboarding) {
    const routes = createOnboardingRoutes(dependencies.onboarding);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  if (journal) for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", journal);
  if (dependencies.noteProjections) {
    const routes = createNoteProjectionRoutes(dependencies.noteProjections);
    for (const prefix of ["", "/api", "/v1"]) app.route(prefix || "/", routes);
  }
  app.get("/livez", (c) => c.json({ status: "ok" }));
  app.get("/readyz", async (c) => {
    c.header("Cache-Control", "no-store");
    if (dependencies.isDraining()) return c.json({ status: "draining" }, 503);
    try {
      await dependencies.checkDatabase();
    } catch {
      return c.json({ status: "unavailable", checks: { database: "unavailable" } }, 503);
    }
    if (!dependencies.migrationComplete) {
      return c.json({ status: "migrating", checks: { database: "ok", routeParity: "pending" } }, 503);
    }
    return c.json({ status: "ok", checks: { database: "ok", routeParity: "ok" } });
  });
  return app;
}
