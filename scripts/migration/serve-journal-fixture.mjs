// Disposable local integration fixture. No application .env is read or changed.
import { readFile, writeFile, stat, rm } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { once } from "node:events";
import { Pool } from "pg";
import { pino } from "pino";
import { serve } from "@hono/node-server";
import { createApi } from "../../dist/apps/api/src/app.js";
import { createAuthService, hashToken } from "../../dist/apps/api/src/modules/auth/service.js";
import { createAuthRepository } from "../../dist/apps/api/src/modules/auth/repository.js";
import { createPasswordHasher } from "../../dist/apps/api/src/modules/auth/passwords.js";
import { createAppRuntimeRepository } from "../../dist/apps/api/src/modules/app-runtime/repository.js";
import { createInstallationRepository } from "../../dist/apps/api/src/modules/official-apps/repository.js";
import { createOfficialCatalog } from "../../dist/apps/api/src/modules/official-apps/catalog.js";
import { createJournalNotes } from "../../dist/apps/api/src/modules/journal/notes.js";
import { createJournalDrawings } from "../../dist/apps/api/src/modules/journal/drawings.js";
import { createAssetRepository } from "../../dist/apps/api/src/modules/journal/asset-repository.js";
import { createJournalAssets } from "../../dist/apps/api/src/modules/journal/assets.js";
import { createS3Store } from "../../dist/apps/api/src/modules/storage/s3-store.js";
import { createStorageJobs } from "../../dist/apps/api/src/modules/storage/jobs.js";
import { withTransaction } from "../../dist/packages/database/src/transaction.js";
import { createRequestBoundary } from "../../dist/packages/runtime/src/request-boundary.js";

const inputPath = process.argv[2];
if (!inputPath || (await stat(inputPath)).mode & 0o077) throw new Error("Provide a mode0600 fixture configuration file");
const config = JSON.parse(await readFile(inputPath, "utf8"));
const dbUrl = new URL(config.databaseUrl), endpoint = new URL(config.s3.endpoint);
if (dbUrl.pathname !== "/misty_hono_journal_fixture_test" || !["127.0.0.1", "localhost"].includes(dbUrl.hostname) ||
  !["127.0.0.1", "localhost", "[::1]"].includes(endpoint.hostname) || endpoint.protocol !== "https:") throw new Error("Fixture requires dedicated local database and HTTPS object endpoint");
const admin = new Pool({ connectionString: config.databaseUrl, max: 2 });
const application = new Pool({ connectionString: config.databaseUrl, options: "-c role=misty_hono_app_test", max: 8 });
await admin.query(`GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
  GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,user_app_installations,app_runtime_sessions,app_data_deletion_jobs,app_install_events,
    space_notes,space_note_links,space_note_assets,space_note_control_outbox,space_drawings,space_drawing_assets,space_drawing_control_outbox,space_events,
    space_library_uploads,space_upload_reservations,space_storage_usage,owner_storage_usage,space_storage_contributions,library_files,library_blobs,
    space_library_audit_events,object_deletion_jobs TO misty_hono_app_test;
  GRANT SELECT,UPDATE ON spaces,space_members,space_conversation_members TO misty_hono_app_test;
  GRANT SELECT ON library_legal_holds,space_conversations,space_rendition_reservations TO misty_hono_app_test;
  GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq,space_library_audit_events_id_seq TO misty_hono_app_test;`);
const store = createS3Store(config.s3), jobs = createStorageJobs(application, store);
const auth = createAuthService({ repository: createAuthRepository(application), passwords: await createPasswordHasher(), deployment: "hosted" });
const username = `fixture_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
const account = await auth.register({ username, email: `${username}@example.invalid`, name: "Disposable Journal fixture", password: randomUUID(), analyticsEnabled: false });
const spaceId = randomUUID(), domainId = randomUUID();
let server, closing = false;
async function close() {
  if (closing) return; closing = true;
  if (server) { server.close(); server.closeAllConnections(); await once(server, "close"); }
  await withTransaction(admin, async (client) => {
    await client.query("DELETE FROM object_deletion_jobs WHERE object_key IN (SELECT object_key FROM space_library_uploads WHERE user_id=$1)", [account.user.id]);
    await client.query("DELETE FROM spaces WHERE id=$1", [spaceId]);
    await client.query("DELETE FROM library_files WHERE security_domain_id=$1", [domainId]);
    await client.query("DELETE FROM library_blobs WHERE security_domain_id=$1", [domainId]);
    await client.query("DELETE FROM security_domains WHERE id=$1", [domainId]);
    await client.query("DELETE FROM users WHERE id=$1", [account.user.id]);
  });
  store.close(); await application.end(); await admin.end(); await rm(config.outputPath, { force: true });
}
try {
  await withTransaction(admin, async (client) => {
    await client.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domainId, account.user.id, spaceId]);
    await client.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Journal fixture',$3)", [spaceId, account.user.id, domainId]);
    await client.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [spaceId, account.user.id]);
  });
  const installations = createInstallationRepository(application);
  await installations.install(account.user.id, createOfficialCatalog().find("journal"));
  const appToken = randomUUID(), controlToken = randomUUID();
  await installations.session(account.user.id, "journal", hashToken(appToken), spaceId);
  const notes = createJournalNotes(application, null), drawings = createJournalDrawings(application, null);
  const note = await notes.create({ userId: account.user.id }, spaceId, "Fixture note");
  const drawing = await drawings.create({ userId: account.user.id }, spaceId, "Fixture drawing");
  const appRuntime = createAppRuntimeRepository(application);
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => closing, migrationComplete: false,
    auth: { service: auth, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }), deployment: "hosted" },
    journal: { auth, appRuntime, notes, drawings, assets: createJournalAssets(createAssetRepository(application), store) }, appRuntime: { repository: appRuntime } });
  app.post("/_fixture/:action", async (c) => {
    if (c.req.header("X-Fixture-Control") !== controlToken) return c.json({ code: "not_found" }, 404);
    if (c.req.param("action") === "revoke") {
      await admin.query("DELETE FROM app_runtime_sessions WHERE token_hash=$1", [hashToken(appToken)]);
    } else if (c.req.param("action") === "expire-and-cleanup") {
      await admin.query("UPDATE space_library_uploads SET expires_at=now()-interval '2 minutes' WHERE user_id=$1 AND state<>'ready'", [account.user.id]);
      await admin.query("UPDATE object_deletion_jobs SET not_before=now()-interval '1 second' WHERE object_key IN (SELECT object_key FROM space_library_uploads WHERE user_id=$1)", [account.user.id]);
      await jobs.runOnce();
    } else return c.json({ code: "not_found" }, 404);
    return c.json({ ok: true });
  });
  server = serve({ fetch: app.fetch, port: 0, hostname: "127.0.0.1" });
  if (!server.listening) await once(server, "listening");
  const address = server.address();
  await writeFile(config.outputPath, JSON.stringify({ apiBase: `http://127.0.0.1:${address.port}/v1`, appToken, controlToken,
    controlBase: `http://127.0.0.1:${address.port}/_fixture`, spaceId, noteId: note.id, drawingId: drawing.id, pid: process.pid }, null, 2), { mode: 0o600, flag: "wx" });
  console.log("Disposable Journal fixture ready; credentials written to its private output file.");
  for (const signal of ["SIGTERM", "SIGINT"]) process.once(signal, () => { void close().catch(() => { process.exitCode = 1; }); });
} catch (error) { await close(); throw error; }
