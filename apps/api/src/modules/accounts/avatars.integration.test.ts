import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PNG } from "pngjs";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createInstallationRepository } from "../official-apps/repository.js";
import { createOfficialCatalog } from "../official-apps/catalog.js";
import { createSpaceRepository } from "../spaces/repository.js";
import { createStorageJobs } from "../storage/jobs.js";
import { createFilesystemByteStore } from "../storage/filesystem-store.js";
import { createAvatarService } from "./avatars.js";
import { AccountUnavailable } from "./repository.js";
import type { ByteObjectStore, ObjectStore } from "../storage/object-store.js";

const admin = createTestDatabase(), users: string[] = [], keys = new Set<string>();
let application: Pool, passwords: PasswordHasher;
const png = new PNG({ width: 2, height: 2 }); png.data.fill(255); const image = PNG.sync.write(png);
const deferred = () => { let resolve!: () => void; const promise = new Promise<void>((done) => { resolve = done; }); return { promise, resolve }; };
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,
      space_setup_integrations,space_creation_requests,space_events,user_app_installations,app_runtime_sessions,app_install_events,app_data_deletion_jobs,object_deletion_jobs TO misty_hono_app_test;
    GRANT SELECT ON self_host_accounts,space_invitations,library_blobs,library_files,library_legal_holds,space_note_assets,space_drawing_assets,space_library_uploads,ai_conversation_attachments TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map((row) => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM object_deletion_jobs WHERE object_key=ANY($1::text[])", [[...keys]]);
  }); users.length = 0; keys.clear();
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" }), runtime = createAppRuntimeRepository(application), installations = createInstallationRepository(application);
  const objects = new Map<string, Buffer>();
  const store: ByteObjectStore & ObjectStore = { putBytes: vi.fn(async (key, data) => { keys.add(key); objects.set(key, Buffer.from(data)); }),
    getBytes: vi.fn(async (key) => objects.get(key) ?? null), delete: vi.fn(async (key) => { objects.delete(key); }), head: async () => null,
    signUpload: async () => { throw new Error("Not used"); }, signDownload: async () => { throw new Error("Not used"); } };
  const service = createAvatarService({ pool: application, store, deployment: "hosted" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }), deployment: "hosted" }, avatars: { auth, appRuntime: runtime, service } });
  const makeAccount = async () => {
    const username = `avatar_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const account = await auth.register({ username, email: `${username}@example.invalid`, name: "Avatar test", password: "test-password", analyticsEnabled: false });
    users.push(account.user.id); keys.add(`avatars/${account.user.id}`); return account;
  };
  const owner = await makeAccount();
  const upload = (data: Buffer = image, token = owner.token, prefix = "") => app.request(`${prefix}/me/avatar`, { method: "PUT", body: new Uint8Array(data), headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/octet-stream" } });
  const get = (path = "/me/avatar", token = owner.token) => app.request(path, { headers: { Authorization: `Bearer ${token}` } });
  const state = async () => (await admin.query("SELECT avatar_version::text AS version,avatar_object_key AS key FROM users WHERE id=$1", [owner.user.id])).rows[0] as { version: string; key: string | null };
  return { app, auth, runtime, installations, service, store, objects, owner, upload, get, state, makeAccount, jobs: createStorageJobs(application, store) };
}

it("publishes exact PNG bytes through all aliases and keeps immutable bytes paired with each version", async () => {
  const f = await fixture(); expect((await f.get()).status).toBe(404);
  const published: string[] = [];
  for (const [index, prefix] of ["", "/api", "/v1"].entries()) {
    const response = await f.upload(image, f.owner.token, prefix); expect(response.status).toBe(200); expect(await response.json()).toEqual({ avatar_version: index + 1 });
    const state = await f.state(); expect(state.key).toMatch(/^avatars\/avatar_[0-9a-f-]{36}$/); published.push(state.key!);
    const loaded = await f.get(`${prefix}/me/avatar`); expect(loaded.status).toBe(200); expect(Buffer.from(await loaded.arrayBuffer())).toEqual(image);
    expect(loaded.headers.get("ETag")).toBe(`"avatar-${index + 1}"`); expect(loaded.headers.get("Cache-Control")).toBe("private, max-age=300"); expect(loaded.headers.get("Content-Type")).toBe("image/png");
    expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE object_key=$1", [state.key])).rowCount).toBe(0);
  }
  expect(new Set(published).size).toBe(3);
  expect((await admin.query("SELECT object_key FROM object_deletion_jobs WHERE object_key=ANY($1::text[])", [published])).rowCount).toBe(2);
  expect((await f.app.request("/me/avatar", { method: "POST", headers: { Authorization: `Bearer ${f.owner.token}` } })).status).toBe(405);
});

it("continues reading legacy keys and preserves pending-deletion avatars until final account deletion", async () => {
  const f = await fixture(), legacy = `avatars/${f.owner.user.id}`; f.objects.set(legacy, image);
  await admin.query("UPDATE users SET avatar_version=9 WHERE id=$1", [f.owner.user.id]);
  expect((await f.get()).headers.get("ETag")).toBe('"avatar-9"');
  await f.upload(); const current = (await f.state()).key!;
  expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE object_key=$1", [legacy])).rowCount).toBe(1);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.owner.user.id]);
  expect((await f.get()).status).toBe(401); expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE object_key=$1", [current])).rowCount).toBe(0);
  await admin.query("DELETE FROM users WHERE id=$1", [f.owner.user.id]);
  expect((await admin.query("SELECT created_by_user_id FROM object_deletion_jobs WHERE object_key=$1", [current])).rows).toEqual([{ created_by_user_id: null }]);
});

it("keeps the old pointer after failed PUT or failed publication and retains cleanup work", async () => {
  const f = await fixture(); await f.upload(); const original = await f.state();
  vi.mocked(f.store.putBytes).mockImplementationOnce(async (key) => { keys.add(key); throw new Error("Synthetic failed PUT"); });
  expect((await f.upload()).status).toBe(500); expect(await f.state()).toEqual(original);
  await admin.query("REVOKE DELETE ON object_deletion_jobs FROM misty_hono_app_test");
  try { expect((await f.upload()).status).toBe(500); expect(await f.state()).toEqual(original); }
  finally { await admin.query("GRANT DELETE ON object_deletion_jobs TO misty_hono_app_test"); }
  expect((await admin.query("SELECT object_key FROM object_deletion_jobs WHERE created_by_user_id=$1", [f.owner.user.id])).rowCount).toBe(2);
  expect((await f.get()).headers.get("ETag")).toBe('"avatar-1"');
});

it("rejects publication after logout during PUT and rejects reads after logout during GET", async () => {
  const f = await fixture(); await f.upload(); const original = await f.state();
  vi.mocked(f.store.putBytes).mockImplementationOnce(async (key, bytes) => { keys.add(key); f.objects.set(key, bytes); await admin.query("DELETE FROM sessions WHERE token_hash=$1", [hashToken(f.owner.token)]); });
  expect((await f.upload()).status).toBe(401); expect(await f.state()).toEqual(original);
  const second = await fixture(); await second.upload();
  vi.mocked(second.store.getBytes).mockImplementationOnce(async () => { await admin.query("DELETE FROM sessions WHERE token_hash=$1", [hashToken(second.owner.token)]); return image; });
  expect((await second.get()).status).toBe(401);
});

it("serializes concurrent publications and rejects excess requests before reading their body", async () => {
  const f = await fixture(), entered = deferred(), release = deferred(); let puts = 0;
  vi.mocked(f.store.putBytes).mockImplementation(async (key, bytes) => { keys.add(key); f.objects.set(key, bytes); if (++puts === 2) entered.resolve(); await release.promise; });
  const requests = [f.upload(), f.upload(image, f.owner.token, "/api")]; await entered.promise;
  let reads = 0;
  const stream = new ReadableStream({ pull() { reads++; } }, { highWaterMark: 0 });
  try {
    const rejected = await f.app.request(new Request("http://test/v1/me/avatar", { method: "PUT", headers: { Authorization: `Bearer ${f.owner.token}` }, body: stream, duplex: "half" } as RequestInit));
    expect(rejected.status).toBe(503); expect(reads).toBe(0);
  } finally { release.resolve(); }
  const responses = await Promise.all(requests); expect(responses.map((response) => response.status)).toEqual([200, 200]);
  expect((await f.state()).version).toBe("2"); expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE created_by_user_id=$1", [f.owner.user.id])).rowCount).toBe(1);
});

it("checks Space membership and App scope again after reading a member's bytes", async () => {
  const f = await fixture(); await f.upload();
  const member = await f.makeAccount(), outside = await f.makeAccount();
  const { space } = await createSpaceRepository(application).create(f.owner.user.id, { name: "Avatar Space", template_id: "blank", integration_providers: [] }, "");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [space.id, member.user.id]);
  const path = `/spaces/${space.id}/members/${f.owner.user.id}/avatar`;
  expect((await f.get(path, member.token)).status).toBe(200); expect((await f.get(path, outside.token)).status).toBe(403);
  await f.installations.install(member.user.id, { ...createOfficialCatalog().find("terminal")!, scopes: ["spaces.read"] });
  const token = randomUUID(); await f.installations.session(member.user.id, "terminal", hashToken(token), space.id);
  expect((await f.get(path, token)).status).toBe(200); expect((await f.get("/me/avatar", token)).status).toBe(401);
  expect((await f.app.request(path, { headers: { Cookie: `misty_session=${token}` } })).status).toBe(401);
  expect((await f.app.request(path, { headers: { Cookie: `misty_session=${member.token}` } })).status).toBe(200);
  vi.mocked(f.store.getBytes).mockImplementationOnce(async () => { await f.installations.uninstall(member.user.id, "terminal"); return image; });
  expect((await f.get(path, token)).status).toBe(401);
  vi.mocked(f.store.getBytes).mockImplementationOnce(async () => { await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [space.id, member.user.id]); return image; });
  expect((await f.get(path, member.token)).status).toBe(403);
});

it("deletes unreferenced avatar objects but protects the currently published object", async () => {
  const f = await fixture(); await f.upload(); const old = (await f.state()).key!; await f.upload(); const current = (await f.state()).key!;
  await admin.query("UPDATE object_deletion_jobs SET not_before=now()-interval '1 minute' WHERE object_key=$1", [old]);
  await admin.query("INSERT INTO object_deletion_jobs(object_key,not_before) VALUES($1,now()-interval '1 minute')", [current]);
  await f.jobs.runOnce(); expect(f.objects.has(old)).toBe(false); expect(f.objects.has(current)).toBe(true);
  expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE object_key=$1", [old])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE object_key=$1", [current])).rowCount).toBe(1);
});

it("bounds PNG inputs and outstanding storage work without affecting other accounts", async () => {
  const f = await fixture(); expect((await f.upload(Buffer.alloc(0))).status).toBe(413);
  expect((await f.upload(Buffer.from("not png"))).status).toBe(400); expect((await f.upload(Buffer.alloc(5 * 1024 * 1024 + 1))).status).toBe(413);
  expect(f.store.putBytes).not.toHaveBeenCalled();
  for (let index = 0; index < 12; index++) { const key = `avatars/avatar_${randomUUID()}`; keys.add(key); await admin.query("INSERT INTO object_deletion_jobs(object_key,not_before,created_by_user_id) VALUES($1,now()+interval '15 minutes',$2)", [key, f.owner.user.id]); }
  expect((await f.upload()).status).toBe(429); expect((await f.state()).version).toBe("0");
  const other = await f.makeAccount(); expect((await f.upload(image, other.token)).status).toBe(200);
});

it("publishes and cleans up real filesystem avatars through authenticated HTTP and restricted PostgreSQL", async () => {
  const f = await fixture(), root = await mkdtemp(join(tmpdir(), "misty-avatar-http-test-"));
  try {
    const local = await createFilesystemByteStore(root);
    vi.mocked(f.store.putBytes).mockImplementation(async (key, data, metadata, signal) => { keys.add(key); await local.putBytes(key, data, metadata, signal); });
    vi.mocked(f.store.getBytes).mockImplementation(local.getBytes);
    vi.mocked(f.store.delete).mockImplementation(local.delete);
    expect((await f.upload()).status).toBe(200); const old = (await f.state()).key!;
    expect((await f.upload()).status).toBe(200); const current = (await f.state()).key!;
    expect(Buffer.from(await (await f.get()).arrayBuffer())).toEqual(image);
    expect(await (await createFilesystemByteStore(root)).getBytes(current, image.length)).toEqual(image);
    await admin.query("UPDATE object_deletion_jobs SET not_before=now()-interval '1 minute' WHERE object_key=$1", [old]);
    await f.jobs.runOnce();
    expect(await local.getBytes(old, image.length)).toBeNull();
    expect(await local.getBytes(current, image.length)).toEqual(image);
  } finally { await rm(root, { recursive: true, force: true }); }
});

it("checks self-host entitlement and administrative disablement again after object transfers", async () => {
  const f = await fixture(), service = createAvatarService({ pool: application, store: f.store, deployment: "self_hosted" });
  const actor = { userId: f.owner.user.id, sessionHash: hashToken(f.owner.token) }, signal = new AbortController().signal;
  await expect(service.upload(actor, image, signal)).rejects.toBeInstanceOf(AccountUnavailable);
  expect(f.store.putBytes).not.toHaveBeenCalled();
  await admin.query("INSERT INTO self_host_accounts(user_id,entitlement_subject,entitlement_expires_at) VALUES($1,$2,now()+interval '1 day')", [f.owner.user.id, `avatar-fixture-${randomUUID()}`]);
  expect(await service.upload(actor, image, signal)).toEqual({ avatar_version: 1 });
  const original = await f.state();
  vi.mocked(f.store.getBytes).mockImplementationOnce(async () => { await admin.query("UPDATE self_host_accounts SET disabled_at=now() WHERE user_id=$1", [actor.userId]); return image; });
  await expect(service.read(actor, signal)).rejects.toBeInstanceOf(AccountUnavailable);
  await admin.query("UPDATE self_host_accounts SET disabled_at=NULL WHERE user_id=$1", [actor.userId]);
  vi.mocked(f.store.putBytes).mockImplementationOnce(async (key, data) => { keys.add(key); f.objects.set(key, data); await admin.query("UPDATE self_host_accounts SET entitlement_expires_at=now()-interval '1 second' WHERE user_id=$1", [actor.userId]); });
  await expect(service.upload(actor, image, signal)).rejects.toBeInstanceOf(AccountUnavailable);
  expect(await f.state()).toEqual(original);
});

it("does not publish objects whose durable staging deadline expired before commit", async () => {
  const f = await fixture(); await f.upload(); const original = await f.state();
  vi.mocked(f.store.putBytes).mockImplementationOnce(async (key, data) => { keys.add(key); f.objects.set(key, data); await admin.query("UPDATE object_deletion_jobs SET not_before=now()-interval '1 second' WHERE object_key=$1", [key]); });
  const response = await f.upload(); expect(response.status).toBe(409);
  expect(await f.state()).toEqual(original);
  expect((await admin.query("SELECT 1 FROM object_deletion_jobs WHERE created_by_user_id=$1", [f.owner.user.id])).rowCount).toBe(1);
});
