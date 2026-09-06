import { loadRuntimeConfig } from "../../../packages/runtime/src/config.js";
import { createLogger } from "../../../packages/runtime/src/logger.js";
import { startHttpServer } from "../../../packages/runtime/src/server.js";
import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { assertRuntimeDatabaseRole } from "../../../packages/database/src/roles.js";
import { createApi } from "./app.js";
import { createTaskRepository } from "./modules/planner/tasks/repository.js";
import { createCalendarRepository } from "./modules/planner/calendar/repository.js";
import { createConnectionRepository } from "./modules/connections/repository.js";
import { loadConnectionCipher } from "./modules/connections/credentials.js";
import { loadConnectionProviders, loadConnectionOAuthClients } from "./modules/connections/config.js";
import { createIntegrationRepository } from "./modules/connections/integrations.js";
import { createLibraryRepository } from "./modules/library/repository.js";
import { createLibraryReauthentication } from "./modules/library/reauthentication.js";
import { createConnectionRevoker } from "./modules/connections/revocation.js";
import { createLegacyTokenBroker } from "./modules/connections/legacy-token-broker.js";
import { createCalendarSourceRepository } from "./modules/planner/calendar/source-repository.js";
import { createRoadmapRepository } from "./modules/planner/roadmaps/repository.js";
import { createCalendarSourceJobs } from "./modules/planner/calendar/source-jobs.js";
import { createCalendarSourceService } from "./modules/planner/calendar/source-service.js";
import { createConnectionTokenBroker } from "./modules/connections/token-broker.js";
import { createOAuthTokenClient } from "./modules/connections/oauth-token.js";
import { createConnectionAuthorizationService } from "./modules/connections/oauth/service.js";
import { loadConnectionAuthorizationConfig } from "./modules/connections/oauth/config.js";
import { createMailRepository } from "./modules/mail/repository.js";
import { createTaskEffects } from "./modules/planner/tasks/effects.js";
import { createMailService } from "./modules/mail/service.js";
import { createAppRuntimeRepository } from "./modules/app-runtime/repository.js";
import { loadServicePublicKeys } from "../../../packages/runtime/src/service-keys.js";
import { createEntitlementRepository } from "./modules/entitlements/repository.js";
import { createEntitlementEffects } from "./modules/entitlements/service.js";
import { createEntitlementExpiry } from "./modules/entitlements/expiry.js";
import { startPollingWorker } from "../../../packages/runtime/src/worker.js";
import { createRequestBoundary } from "../../../packages/runtime/src/request-boundary.js";
import { createAuthRepository } from "./modules/auth/repository.js";
import { createAuthService } from "./modules/auth/service.js";
import { createPasswordHasher } from "./modules/auth/passwords.js";
import { createSelfHostProofVerifier, loadSelfHostPublicKeys } from "./modules/self-host/proof.js";
import { loadHandoffConfig } from "./modules/auth/handoff/config.js";
import { createHandoffRepository } from "./modules/auth/handoff/repository.js";
import { createHandoffService } from "./modules/auth/handoff/service.js";
import { createMailjetSender, loadMailjetConfig } from "../../../packages/email/src/mailjet.js";
import { createRecoveryRepository } from "./modules/auth/recovery/repository.js";
import { createRecoveryService } from "./modules/auth/recovery/service.js";
import { loadRecoveryConfig } from "./modules/auth/recovery/config.js";
import { loadRecoveryTokenKeys } from "./modules/auth/recovery/token-keys.js";
import { createRecoveryJobs } from "./modules/auth/recovery/jobs.js";
import { createAuthCleanup } from "./modules/auth/cleanup.js";
import { loadInstanceConfig } from "./modules/self-host/config.js";
import { createSelfHostRepository } from "./modules/self-host/repository.js";
import { createSelfHostService } from "./modules/self-host/service.js";
import { createSelfHostIssuer, loadSelfHostSigningConfig } from "./modules/self-host/issuer.js";
import { createSelfHostEligibility } from "./modules/self-host/eligibility.js";
import { createAccountExport } from "./modules/accounts/export.js";
import { createAccountRepository } from "./modules/accounts/repository.js";
import { createAccountSummary } from "./modules/accounts/summary.js";
import { loadBillingCommandClient } from "./modules/billing/command-client.js";
import { createBillingUsage } from "./modules/billing/usage.js";
import { createBillingCommands } from "./modules/billing/commands.js";
import { loadBillingSummaryClient } from "./modules/billing/summary-client.js";
import { createAvatarService } from "./modules/accounts/avatars.js";
import { loadInvitationConfig } from "./modules/spaces/invitations/config.js";
import { createInvitationRepository } from "./modules/spaces/invitations/repository.js";
import { createInvitationJobs } from "./modules/spaces/invitations/jobs.js";
import { loadTemplateProviders } from "./modules/spaces/config.js";
import { createSpaceRepository } from "./modules/spaces/repository.js";
import { createOnboardingRepository } from "./modules/onboarding/repository.js";
import { createOfficialCatalog } from "./modules/official-apps/catalog.js";
import { createAppPurgeJobs } from "./modules/official-apps/purge-jobs.js";
import { createInstallationRepository } from "./modules/official-apps/repository.js";
import { loadCollaborationConfig } from "./modules/collaboration/config.js";
import { createCollaborationTickets } from "./modules/collaboration/tickets.js";
import { createJournalNotes } from "./modules/journal/notes.js";
import { createJournalDrawings } from "./modules/journal/drawings.js";
import { createNoteProjectionRepository } from "./modules/collaboration/projections.js";
import { loadS3Config } from "./modules/storage/s3-config.js";
import { createS3Store } from "./modules/storage/s3-store.js";
import { loadFilesystemConfig } from "./modules/storage/filesystem-config.js";
import { createFilesystemByteStore } from "./modules/storage/filesystem-store.js";
import { createLibraryDownloads } from "./modules/library/downloads.js";
import { createLibraryDownloadRepository } from "./modules/library/download-repository.js";
import { createLibraryMutations } from "./modules/library/mutations.js";
import { createLibraryOrganization } from "./modules/library/organization.js";
import { createEgressGuard, loadEgressBudget } from "./modules/storage/egress.js";
import { createAssetRepository } from "./modules/journal/asset-repository.js";
import { createJournalAssets } from "./modules/journal/assets.js";
import { createStorageJobs } from "./modules/storage/jobs.js";
import { loadAssetConfig } from "./modules/journal/asset-config.js";
import { createControlSender } from "./modules/collaboration/control.js";
import { createControlJobs } from "./modules/collaboration/control-jobs.js";
import { createAssetRetention } from "./modules/journal/asset-retention.js";
import { createDocumentRetention } from "./modules/journal/document-retention.js";

const config = loadRuntimeConfig("api", process.env);
const logger = createLogger(config);
const paymentsKeysFile = process.env.PAYMENTS_VERIFICATION_KEYS_FILE;
if (paymentsKeysFile && config.deployment !== "hosted") throw new Error("Payment entitlement delivery is hosted-only");
const paymentsKeys = paymentsKeysFile ? await loadServicePublicKeys(paymentsKeysFile) : undefined;
const boundary = createRequestBoundary({ allowedOrigins: (process.env.MISTY_ALLOWED_ORIGINS ?? "").split(",").filter(Boolean),
  trustProxyHeaders: /^(true|1)$/i.test(process.env.TRUST_PROXY_HEADERS ?? ""),
  trustedProxyCidrs: (process.env.TRUSTED_PROXY_CIDRS ?? "").split(",").filter(Boolean),
});
const passwords = await createPasswordHasher();
const connectionCipher = loadConnectionCipher(process.env.SPACE_LINK_ENCRYPTION_KEY);
const connectionClients = loadConnectionOAuthClients(process.env);
const connectionAuthorizationConfig = loadConnectionAuthorizationConfig(process.env);
if (config.environment === "production" && Object.keys(connectionClients).length && !connectionAuthorizationConfig) throw new Error("Connected-account OAuth requires MISTY_PUBLIC_API_URL");
if (config.environment === "production" && !connectionCipher) throw new Error("Production API requires SPACE_LINK_ENCRYPTION_KEY");
const hosted = config.deployment === "hosted";
const billingCommands = await loadBillingCommandClient(process.env, config);
const billingSummary = await loadBillingSummaryClient(process.env, config);
const selfHostSigning = hosted ? loadSelfHostSigningConfig(process.env) : null;
const collaborationConfig = loadCollaborationConfig(process.env);
if (config.environment === "production" && !collaborationConfig) throw new Error("Production API requires Journal collaboration configuration");
const collaborationTickets = collaborationConfig ? createCollaborationTickets(collaborationConfig) : null;
const handoffConfig = loadHandoffConfig(hosted ? process.env : {});
const mailjetConfig = hosted || process.env.MISTY_INVITATION_TOKEN_KEYS_FILE ? loadMailjetConfig(process.env) : null;
const invitationConfig = await loadInvitationConfig(process.env);
if (hosted && config.environment === "production" && !mailjetConfig) throw new Error("Production password recovery requires Mailjet configuration");
const recoveryConfig = loadRecoveryConfig(hosted ? process.env : {});
const recoveryKeyFile = process.env.AUTH_RECOVERY_TOKEN_KEYS_FILE;
const recoveryKeys = hosted && recoveryKeyFile ? await loadRecoveryTokenKeys(recoveryKeyFile) : undefined;
if (hosted && config.environment === "production" && !recoveryKeys) throw new Error("Production password recovery requires AUTH_RECOVERY_TOKEN_KEYS_FILE");
const verifySelfHostProof = config.deployment === "self_hosted" ? createSelfHostProofVerifier(await loadSelfHostPublicKeys(process.env.MISTY_SELF_HOST_ENTITLEMENT_PUBLIC_KEYS)) : undefined;
const database = createDatabasePool(process.env, "api");
const filesystemDirectory = loadFilesystemConfig(process.env, config);
const storageConfig = filesystemDirectory ? null : loadS3Config(process.env);
const objectStore = storageConfig ? createS3Store(storageConfig) : null;
const avatarStore = filesystemDirectory ? await createFilesystemByteStore(filesystemDirectory) : objectStore;
database.on("error", (error) => logger.error({ errorType: error.name }, "idle database connection failed"));
const recoveryJobs = recoveryKeys ? createRecoveryJobs({ pool: database, keys: recoveryKeys, logger, config: recoveryConfig, send: createMailjetSender(mailjetConfig) }) : undefined;
const authService = createAuthService({ repository: createAuthRepository(database), passwords, deployment: config.deployment, ...(verifySelfHostProof ? { verifySelfHostProof } : {}) });
const appRuntimeRepository = createAppRuntimeRepository(database);
const connectionBroker = connectionCipher ? createConnectionTokenBroker({ pool: database, cipher: connectionCipher,
  refresh: createOAuthTokenClient(connectionClients).refresh }) : null;
const calendarJobsEnabled = process.env.MISTY_NATIVE_CALENDAR_JOBS === "1";
if (calendarJobsEnabled && !connectionCipher) throw new Error("Calendar jobs require SPACE_LINK_ENCRYPTION_KEY");
const calendarSourceService = createCalendarSourceService({ repository: createCalendarSourceRepository(database),
  broker: connectionCipher ? createLegacyTokenBroker({ pool: database, cipher: connectionCipher, refresh: createOAuthTokenClient(connectionClients).refresh }) : null,
  watchAddress: calendarJobsEnabled && connectionAuthorizationConfig?.apiBase.startsWith("https:") ? `${connectionAuthorizationConfig.apiBase}/provider-callbacks/google/calendar` : null });
const calendarJobs = createCalendarSourceJobs(database, calendarSourceService);
const selfHostService = createSelfHostService({ repository: createSelfHostRepository(database), passwords,
  config: loadInstanceConfig(process.env, config.deployment), verify: verifySelfHostProof ?? (async () => { throw new Error("Self-host proof unavailable in hosted mode"); }) });
let draining = false;
const app = createApi({
  logger,
  checkDatabase: async () => {
    await assertRuntimeDatabaseRole(database, "api");
    const query = { text: "SELECT 1", query_timeout: 2000 };
    await database.query(query);
  },
  isDraining: () => draining,
  migrationComplete: false,
  selfHost: { service: selfHostService, auth: authService, boundary, deployment: config.deployment },
  billing: { auth: authService, commands: createBillingCommands({ pool: database, client: billingCommands, deployment: config.deployment }), usage: createBillingUsage({ pool: database, billing: billingSummary, deployment: config.deployment }) },
  accounts: { auth: authService, repository: createAccountRepository(database), export: createAccountExport({ pool: database, passwords, tickets: collaborationTickets, store: objectStore }), summary: createAccountSummary({ pool: database, billing: billingSummary, deployment: config.deployment }) },
  library: { auth: authService, repository: createLibraryRepository(database), reauthenticate: createLibraryReauthentication(database, passwords), mutations: createLibraryMutations(database), organization: createLibraryOrganization(database),
    downloads: createLibraryDownloads({ repository: createLibraryDownloadRepository(database), store: avatarStore,
      egress: createEgressGuard(loadEgressBudget(process.env)), downloadTtlMs: loadAssetConfig(process.env).downloadTtlMs }) },
  avatars: { auth: authService, appRuntime: appRuntimeRepository, service: createAvatarService({ pool: database, store: avatarStore, deployment: config.deployment }) },
  officialApps: { auth: authService, catalog: createOfficialCatalog(), repository: createInstallationRepository(database) },
  invitations: { auth: authService, repository: createInvitationRepository(database, mailjetConfig ? invitationConfig : null) },
  spaces: { auth: authService, appRuntime: appRuntimeRepository, repository: createSpaceRepository(database), providers: loadTemplateProviders(process.env) },
  planner: { auth: authService, appRuntime: appRuntimeRepository, tasks: createTaskRepository(database), calendar: createCalendarRepository(database),
    calendarSources: calendarSourceService, roadmaps: createRoadmapRepository(database) },
  calendarCallbacks: calendarJobs,
  connections: { auth: authService, appRuntime: appRuntimeRepository, repository: createConnectionRepository(database, connectionCipher ? createConnectionRevoker(connectionCipher) : null), providers: loadConnectionProviders(process.env),
    integrations: createIntegrationRepository(database, connectionCipher), integrationProviders: loadTemplateProviders(process.env),
    authorization: connectionCipher && connectionAuthorizationConfig ? createConnectionAuthorizationService({ pool: database, cipher: connectionCipher, clients: connectionClients, config: connectionAuthorizationConfig, deployment: config.deployment }) : null },
  mail: { auth: authService, appRuntime: appRuntimeRepository, service: createMailService({ repository: createMailRepository(database), broker: connectionBroker }) },
  onboarding: { auth: authService, catalog: createOfficialCatalog(), repository: createOnboardingRepository(database) },
  journal: { auth: authService, appRuntime: appRuntimeRepository, notes: createJournalNotes(database, collaborationTickets), drawings: createJournalDrawings(database, collaborationTickets),
    assets: createJournalAssets(createAssetRepository(database), objectStore, () => new Date(), loadAssetConfig(process.env)) },
  noteProjections: { config: collaborationConfig, apply: createNoteProjectionRepository(database) },
  ...(hosted ? { selfHostIssuance: { auth: authService, boundary, eligibility: createSelfHostEligibility(database),
    issuer: selfHostSigning ? createSelfHostIssuer(selfHostSigning) : null } } : {}),
  auth: { boundary, deployment: config.deployment, service: authService, ...(hosted ? {
    recovery: createRecoveryService({ repository: createRecoveryRepository(database), passwords, logger, config: recoveryConfig,
      jobs: recoveryJobs ?? { enqueue: async () => { throw new Error("Password recovery is not configured"); } } }),
    handoff: createHandoffService({ repository: createHandoffRepository(database), config: handoffConfig }),
  } : {}) },
  appRuntime: { repository: appRuntimeRepository },
  ...(paymentsKeys ? { entitlements: {
    publicKeys: paymentsKeys,
    repository: createEntitlementRepository({ pool: database, ...createEntitlementEffects() }),
  } } : {}),
});
const expiry = createEntitlementExpiry({ pool: database });
const workers = paymentsKeys && process.env.MISTY_NATIVE_ENTITLEMENT_EXPIRY_JOBS === "1" ? [startPollingWorker({ name: "entitlement-expiry", logger, idleMilliseconds: 15000,
  runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return expiry.runOnce(); },
})] : [];
if (recoveryJobs) workers.push(startPollingWorker({ name: "password-recovery", logger, idleMilliseconds: 1000,
  runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return recoveryJobs.runOnce(); },
}));
if (process.env.MISTY_NATIVE_APP_PURGE_JOBS === "1") {
  const appPurge = createAppPurgeJobs(database);
  workers.push(startPollingWorker({ name: "app-data-purge", logger, idleMilliseconds: 5000,
    runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return appPurge.runOnce(); },
  }));
}
if (invitationConfig && mailjetConfig && process.env.MISTY_NATIVE_INVITATION_JOBS === "1") {
  const invitationJobs = createInvitationJobs(database, invitationConfig, createMailjetSender(mailjetConfig));
  workers.push(startPollingWorker({ name: "space-invitation-delivery", logger, idleMilliseconds: 5000,
    runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return invitationJobs.runOnce(); },
  }));
}
if (calendarJobsEnabled) {
  const worker = startPollingWorker({ name: "calendar-reconciliation", logger, idleMilliseconds: 5000,
    runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return calendarJobs.runOnce(); },
  });
  workers.push({ stop: async () => { calendarJobs.abort(); await worker.stop(); } });
}
const authCleanup = createAuthCleanup(database);
if (process.env.MISTY_NATIVE_TASK_EFFECTS_JOBS === "1") {
  const taskEffects = createTaskEffects(database);
  workers.push(startPollingWorker({ name: "task-effects", logger,
    runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return taskEffects.runOnce(); },
  }));
}
if (collaborationConfig && process.env.MISTY_NATIVE_JOURNAL_JOBS === "1") {
  const controlJobs = createControlJobs(database, createControlSender(collaborationConfig, config.deployment));
  const documentRetention = createDocumentRetention(database);
  workers.push(startPollingWorker({ name: "journal-control", logger, idleMilliseconds: 5000,
    runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); const delivered = await controlJobs.runOnce(); return await documentRetention.runOnce() || delivered; },
  }));
}
if (avatarStore && process.env.MISTY_NATIVE_STORAGE_JOBS === "1") {
  const storageJobs = createStorageJobs(database, avatarStore);
  const assetRetention = createAssetRetention(database);
  workers.push(startPollingWorker({ name: "storage-cleanup", logger, idleMilliseconds: 5000,
    runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); const retained = await assetRetention.runOnce(); return (await storageJobs.runOnce()) > 0 || retained; },
  }));
}
workers.push(startPollingWorker({ name: "authentication-cleanup", logger, idleMilliseconds: 60000,
  runOnce: async () => { await assertRuntimeDatabaseRole(database, "api"); return authCleanup.runOnce(); },
}));
const runtime = startHttpServer({
  app, config, logger,
  markDraining: () => { draining = true; },
  drain: async () => { await Promise.all(workers.map((worker) => worker.stop())); objectStore?.close(); await database.end(); },
});
runtime.server.on("error", (error) => {
  logger.fatal({ errorType: error.name }, "server failed");
  process.exitCode = 1;
  void Promise.all(workers.map((worker) => worker.stop())).then(() => { objectStore?.close(); return database.end(); });
});
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.once(signal, () => {
    void runtime.close().catch((error: unknown) => {
      logger.error({ errorType: error instanceof Error ? error.name : "Unknown" }, "shutdown failed");
      process.exitCode = 1;
    });
  });
}
