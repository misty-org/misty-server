import { generateKeyPairSync, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { decodeJwt } from "jose";
import { parseMethodResult } from "@misty/contracts";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createInstallationRepository } from "../official-apps/repository.js";
import { createOfficialCatalog } from "../official-apps/catalog.js";
import { hashToken } from "../auth/service.js";
import { createCollaborationTickets } from "../collaboration/tickets.js";
import { createNoteProjectionRepository, signCollaborationPayload } from "../collaboration/projections.js";
import { createJournalNotes } from "./notes.js";
import { createJournalDrawings } from "./drawings.js";
import { createAssetRepository } from "./asset-repository.js";
import { createJournalAssets } from "./assets.js";
import type { ObjectMetadata, ObjectStore } from "../storage/object-store.js";
import { createAssetRetention } from "./asset-retention.js";
import { createDocumentRetention } from "./document-retention.js";
import { createControlJobs } from "../collaboration/control-jobs.js";
import { createStorageJobs } from "../storage/jobs.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
const objects = new Map<string, ObjectMetadata>();
let application: Pool, passwords: PasswordHasher;
const collaborationConfig = { origin: "https://collaboration.example.invalid", privateKey: generateKeyPairSync("ed25519").privateKey,
  roomSalt: Buffer.alloc(32, 10), controlSecret: Buffer.alloc(32, 11), projectionSecret: Buffer.alloc(32, 12), previousProjectionSecret: Buffer.alloc(32, 13), issuer: "misty-api", audience: "misty-journal-collab" };
const signer = createCollaborationTickets(collaborationConfig);
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,user_app_installations,app_runtime_sessions,app_personal_records,app_data_deletion_jobs,app_install_events,
      space_notes,space_note_links,space_note_assets,space_note_control_outbox,space_drawings,space_drawing_control_outbox,space_events TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON space_drawing_assets,space_library_uploads,space_upload_reservations,space_storage_usage,owner_storage_usage,
      space_storage_contributions,library_files,library_blobs,space_library_audit_events,object_deletion_jobs TO misty_hono_app_test;
    GRANT SELECT ON library_legal_holds,space_library_items,space_message_attachments,library_item_versions,library_derivatives,library_exports,space_rendition_reservations,ai_conversation_attachments TO misty_hono_app_test;
    GRANT USAGE,SELECT ON space_library_audit_events_id_seq TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON spaces,space_members,space_conversation_members TO misty_hono_app_test;
    GRANT SELECT ON space_conversations TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("DELETE FROM library_legal_holds WHERE space_id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM library_files WHERE security_domain_id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM library_blobs WHERE security_domain_id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM object_deletion_jobs WHERE object_key=ANY($1::text[])", [[...objects.keys()]]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  });
  spaces.length = 0; domains.length = 0; users.length = 0;
  objects.clear();
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture(configured = true) {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const notes = createJournalNotes(application, configured ? signer : null), drawings = createJournalDrawings(application, configured ? signer : null);
  const appRuntime = createAppRuntimeRepository(application), installations = createInstallationRepository(application);
  const boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const store: ObjectStore = {
    head: async (key) => objects.get(key) ?? null,
    delete: async (key) => { objects.delete(key); },
    signUpload: async (key, metadata, expires) => { objects.set(key, metadata); return { url: `https://r2.example.invalid/${key}`, method: "PUT", headers: {}, expires_at: expires.toISOString() }; },
    signDownload: async (key, filename, expires) => ({ url: `https://r2.example.invalid/${key}`, filename, expires_at: expires.toISOString() }),
  };
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary, deployment: "hosted" }, journal: { auth, appRuntime, notes, drawings, assets: createJournalAssets(createAssetRepository(application), store) }, appRuntime: { repository: appRuntime },
    noteProjections: { config: configured ? collaborationConfig : null, apply: createNoteProjectionRepository(application) } });
  const account = async () => {
    const username = `journal_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username, email: `${username}@example.invalid`, name: "Journal test", password: "test-password", analyticsEnabled: false });
    users.push(result.user.id); return result;
  };
  const owner = await account(), member = await account(), outsider = await account();
  const spaceId = randomUUID(), domain = randomUUID();
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, owner.user.id, spaceId]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Journal test',$3)", [spaceId, owner.user.id, domain]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'member')", [spaceId, owner.user.id, member.user.id]);
  }); domains.push(domain); spaces.push(spaceId);
  const request = (path: string, method = "GET", body?: unknown, token = owner.token) => app.request(path, { method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}) });
  const base = `/v1/spaces/${spaceId}`;
  const create = async (kind: "notes" | "drawings", token = owner.token, title = "Created") => {
    const response = await request(`${base}/${kind}`, "POST", { title }, token);
    expect(response.status, await response.clone().text()).toBe(201); return response.json();
  };
  const appToken = async (scopes: string[], user = owner) => {
    const app = { ...createOfficialCatalog().find("journal")!, scopes };
    await installations.install(user.user.id, app);
    const token = randomUUID(); await installations.session(user.user.id, app.id, hashToken(token), spaceId); return token;
  };
  const rpc = (token: string, method: string, params: unknown = {}, prefix = "/v1") => request(`${prefix}/app-runtime/rpc`, "POST", { protocol: 2, method, params }, token);
  const project = (input: unknown, options: { secret?: Buffer; age?: number; signature?: string; prefix?: string } = {}) => {
    const body = JSON.stringify(input), timestamp = String(Math.floor(Date.now() / 1000) - (options.age ?? 0));
    return app.request(`${options.prefix ?? "/v1"}/internal/journal/note-projections`, { method: "POST", body, headers: { "Content-Type": "application/json",
      "X-Misty-Timestamp": timestamp, "X-Misty-Signature": options.signature ?? signCollaborationPayload(options.secret ?? collaborationConfig.projectionSecret, timestamp, Buffer.from(body)) } });
  };
  return { app, auth, notes, drawings, owner, member, outsider, spaceId, base, request, create, appToken, rpc, project, store };
}

async function assetFixture(kind: "note" | "drawing") {
  const f = await fixture(), parent = await f.create(`${kind}s`), token = await f.appToken([`${kind}s.read`, `${kind}s.write`]);
  const input = { filename: "../image.png", mime_type: "image/png", byte_size: 2048, sha256: "a".repeat(64), ...(kind === "drawing" ? { file_id: "scene-image-1" } : {}) };
  const path = { [`${kind}ID`]: parent.id };
  const reserve = async () => {
    const response = await f.rpc(token, `${kind}s.assets.reserve`, { path, body: input });
    expect(response.status, await response.clone().text()).toBe(201);
    const result = await response.json(); expect(() => parseMethodResult(`${kind}s.assets.reserve`, result)).not.toThrow();
    return { ...result, key: new URL(result.transfer.url).pathname.slice(1) };
  };
  const finish = (reservation: Awaited<ReturnType<typeof reserve>>, parentId = parent.id, credential = reservation.finalize.headers["X-Misty-Library-Upload-Token"]) => f.app.request("/v1/app-runtime/rpc", {
    method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "X-Misty-Library-Upload-Token": credential },
    body: JSON.stringify({ protocol: 2, method: `${kind}s.assets.finalize`, params: { path: { [`${kind}ID`]: parentId, uploadID: reservation.upload.id } } }),
  });
  return { ...f, token, input, parent, path, reserve, finish };
}

it("runs six native asset RPC methods with exact SDK results, concurrent retry and private descriptors", async () => {
  // Different fixture per test would repeat identical assertions; each resource
  // gets independent real memberships, grants, records and logical quota.
  for (const kind of ["note", "drawing"] as const) {
    const f = await assetFixture(kind), reserved = await f.reserve();
    expect(JSON.stringify(reserved.upload)).not.toContain("object_key");
    expect(JSON.stringify(reserved.upload)).not.toContain("upload_token_hash");
    expect((await f.finish(reserved, "wrong-parent")).status).toBe(404);
    expect((await f.finish(reserved, f.parent.id, "incorrect")).status).toBe(403);
    const results = await Promise.all([f.finish(reserved), f.finish(reserved)]);
    for (const result of results) expect(result.status, await result.clone().text()).toBe(200);
    const first = await results[0]!.json(), second = await results[1]!.json();
    expect(() => parseMethodResult(`${kind}s.assets.finalize`, first)).not.toThrow();
    expect(first).toEqual(second);
    const asset = first[`${kind}_asset`];
    const downloaded = await f.rpc(f.token, `${kind}s.assets.download`, { path: { ...f.path, assetID: asset.id } });
    expect(downloaded.status, await downloaded.clone().text()).toBe(200);
    const descriptor = await downloaded.json();
    expect(() => parseMethodResult(`${kind}s.assets.download`, descriptor)).not.toThrow();
    expect(descriptor).toMatchObject({ byte_size: 2048, mime_type: "image/png", filename: "image.png", sha256: "a".repeat(64) });
    const usage = (await admin.query("SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0];
    expect(usage).toEqual({ used_bytes: "2048", reserved_bytes: "0" });
    expect((await admin.query("SELECT count(*) FROM space_storage_contributions WHERE space_id=$1", [f.spaceId])).rows[0].count).toBe("1");
  }
});

it("releases mismatched asset reservations and repairs a missing deduplicated object without extra physical blobs", async () => {
  const f = await assetFixture("note"), bad = await f.reserve();
  objects.set(bad.key, { byteSize: 2049, mimeType: "image/png", sha256: "a".repeat(64) });
  expect((await f.finish(bad)).status).toBe(422);
  expect((await admin.query("SELECT reserved_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0].reserved_bytes).toBe("0");
  expect((await admin.query("SELECT not_before FROM object_deletion_jobs WHERE object_key=$1", [bad.key])).rows[0].not_before.getTime()).toBeGreaterThan(Date.now()+29*60000);
  const first = await f.reserve(); expect((await f.finish(first)).status).toBe(200);
  const duplicate = await f.reserve(); expect((await f.finish(duplicate)).status).toBe(200);
  expect((await admin.query("SELECT count(*) FROM library_blobs WHERE security_domain_id IN (SELECT security_domain_id FROM spaces WHERE id=$1)", [f.spaceId])).rows[0].count).toBe("1");
  const originalHead = f.store.head;
  f.store.head = async (key) => key === first.key ? null : originalHead(key);
  const repair = await f.reserve(); expect((await f.finish(repair)).status).toBe(200);
  expect((await admin.query("SELECT r2_object_key FROM library_blobs WHERE security_domain_id IN (SELECT security_domain_id FROM spaces WHERE id=$1)", [f.spaceId])).rows[0].r2_object_key).toBe(repair.key);
  expect((await admin.query("SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0]).toEqual({ used_bytes: "6144", reserved_bytes: "0" });
});

it("rechecks the app credential after storage verification and rolls back asset/accounting on event failure", async () => {
  const f = await assetFixture("drawing"), reserved = await f.reserve();
  const originalHead = f.store.head;
  f.store.head = async (key) => {
    await admin.query("DELETE FROM app_runtime_sessions WHERE token_hash=$1", [hashToken(f.token)]);
    return originalHead(key);
  };
  expect((await f.finish(reserved)).status).toBe(401);
  expect((await admin.query("SELECT state FROM space_library_uploads WHERE id=$1", [reserved.upload.id])).rows[0].state).toBe("initiated");
  f.store.head = originalHead;
  // Reissue the same fixture token only in the test database, then exercise a
  // database failure after file creation but before the transaction commits.
  await createInstallationRepository(application).session(f.owner.user.id, "journal", hashToken(f.token), f.spaceId);
  await admin.query("REVOKE INSERT ON space_events FROM misty_hono_app_test");
  try { expect((await f.finish(reserved)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM space_storage_contributions WHERE space_id=$1", [f.spaceId])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0]).toEqual({ used_bytes: "0", reserved_bytes: "2048" });
  expect((await f.finish(reserved)).status).toBe(200);
});

it("serializes personal capacity across Spaces and Space capacity across contributors", async () => {
  const f = await assetFixture("note"), other = await fixture(), otherParent = await other.create("notes");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [other.spaceId, f.owner.user.id]);
  const otherToken = randomUUID();
  await createInstallationRepository(application).session(f.owner.user.id, "journal", hashToken(otherToken), other.spaceId);
  await admin.query(`INSERT INTO space_storage_contributions(id,space_id,user_id,source_kind,source_id,logical_bytes,state)
    VALUES($1,$2,$3,'import',$1,1999997952,'active')`, [randomUUID(), f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_storage_usage(space_id,used_bytes) VALUES($1,1999997952) ON CONFLICT(space_id) DO UPDATE SET used_bytes=EXCLUDED.used_bytes", [f.spaceId]);
  const results = await Promise.all([
    f.rpc(f.token, "notes.assets.reserve", { path: f.path, body: f.input }),
    other.rpc(otherToken, "notes.assets.reserve", { path: { noteID: otherParent.id }, body: f.input }),
  ]);
  expect(results.map((r) => r.status).sort()).toEqual([201, 409]);
  expect(await results.find((r) => r.status === 409)!.json()).toMatchObject({ code: "owner_storage_quota_exceeded", reason: "personal_storage_limit_reached" });
  // A second contributor has personal room, but cannot overfill the destination.
  await admin.query("INSERT INTO space_storage_usage(space_id,used_bytes) VALUES($1,2000000000) ON CONFLICT(space_id) DO UPDATE SET used_bytes=EXCLUDED.used_bytes", [other.spaceId]);
  const ownerToken = await other.appToken(["notes.read", "notes.write"]);
  const denied = await other.rpc(ownerToken, "notes.assets.reserve", { path: { noteID: otherParent.id }, body: f.input });
  expect(denied.status).toBe(409);
  expect(await denied.json()).toMatchObject({ reason: "space_storage_limit_reached" });
});

it("expires abandoned uploads and durably retries object deletion without deleting referenced blobs", async () => {
  const f = await assetFixture("note"), abandoned = await f.reserve(), ready = await f.reserve();
  expect((await f.finish(ready)).status).toBe(200);
  await admin.query("UPDATE space_library_uploads SET expires_at=now()-interval '2 minutes' WHERE id=$1", [abandoned.upload.id]);
  const deletions: string[] = [];
  f.store.delete = async (key) => { deletions.push(key); throw new Error("test unavailable store"); };
  const jobs = createStorageJobs(application, f.store);
  await jobs.runOnce();
  expect((await admin.query("SELECT state FROM space_library_uploads WHERE id=$1", [abandoned.upload.id])).rows[0].state).toBe("expired");
  expect((await admin.query("SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0]).toEqual({ used_bytes: "2048", reserved_bytes: "0" });
  expect(deletions).toEqual([abandoned.key]);
  const queued = (await admin.query("SELECT attempts,lease_id,not_before FROM object_deletion_jobs WHERE object_key=$1", [abandoned.key])).rows[0];
  expect(queued).toMatchObject({ attempts: 1, lease_id: null }); expect(queued.not_before.getTime()).toBeGreaterThan(Date.now());
  await admin.query("UPDATE object_deletion_jobs SET not_before=now()-interval '1 second' WHERE object_key=$1", [abandoned.key]);
  await admin.query("INSERT INTO object_deletion_jobs(object_key,not_before) VALUES($1,now()-interval '1 second')", [ready.key]);
  f.store.delete = async (key) => { deletions.push(key); };
  await Promise.all([jobs.runOnce(), createStorageJobs(application, f.store).runOnce()]);
  expect(deletions).toEqual([abandoned.key, abandoned.key]);
  expect((await admin.query("SELECT count(*) FROM object_deletion_jobs WHERE object_key=$1", [abandoned.key])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT count(*) FROM object_deletion_jobs WHERE object_key=$1", [ready.key])).rows[0].count).toBe("1");
});

it("runs native note and drawing CRUD over HTTP with shared-schema responses and membership roles", async () => {
  const f = await fixture();
  for (const kind of ["notes", "drawings"] as const) {
    const created = await f.create(kind, f.member.token, kind === "drawings" ? "   " : "  Note title  ");
    expect(() => parseMethodResult(`${kind}.create`, created)).not.toThrow();
    expect(created).toMatchObject({ role: "creator", can_delete: true, audience_kind: "space", title: kind === "drawings" ? "Untitled drawing" : "  Note title  " });
    const read = await (await f.request(`${f.base}/${kind}/${created.id}`)).json();
    expect(read).toMatchObject({ role: "editor", can_delete: true });
    expect((await f.request(`${f.base}/${kind}/${created.id}`, "GET", undefined, f.outsider.token)).status).toBe(404);
    for (const prefix of ["", "/api", "/v1"]) expect((await f.request(`${prefix}/spaces/${f.spaceId}/${kind}`)).status).toBe(200);
    if (kind === "notes") {
      expect((await f.request(`${f.base}/notes/${created.id}/metadata`, "PATCH", { shared_tags: ["tag"] })).status).toBe(200);
      expect((await admin.query("SELECT shared_tags FROM space_notes WHERE id=$1", [created.id])).rows[0].shared_tags).toEqual(["tag"]);
    } else expect((await f.request(`${f.base}/drawings/${created.id}`, "PATCH", { title: "Renamed" })).status).toBe(200);
    expect((await f.request(`${f.base}/${kind}/${created.id}`, "DELETE")).status).toBe(204);
    expect((await f.request(`${f.base}/${kind}/${created.id}`)).status).toBe(404);
    expect((await f.request(`${f.base}/${kind}/${created.id}`, "DELETE")).status).toBe(kind === "notes" ? 204 : 404);
  }
});

it("enforces app scopes and bound Space inside real RPC and ticket issuance", async () => {
  const f = await fixture(), token = await f.appToken(["notes.read", "notes.write", "drawings.read", "drawings.write"]);
  for (const kind of ["notes", "drawings"] as const) {
    const response = await f.rpc(token, `${kind}.create`, { body: { title: "RPC" } });
    expect(response.status, await response.clone().text()).toBe(201); const created = await response.json();
    const path = kind === "notes" ? { noteID: created.id } : { drawingID: created.id };
    const ticket = await f.rpc(token, `${kind}.collaboration.ticket`, { path });
    expect(ticket.status, await ticket.clone().text()).toBe(201);
    expect(ticket.headers.get("Cache-Control")).toBe("private, no-store");
    const issued = await ticket.json(); expect(() => parseMethodResult(`${kind}.collaboration.ticket`, issued)).not.toThrow();
    const claims = decodeJwt(issued.ticket);
    expect(claims).toMatchObject({ sub: f.owner.user.id, space_id: f.spaceId, resource_id: created.id, resource_type: kind === "notes" ? "note" : "drawing", role: "creator", acl_version: 1 });
    expect(claims.exp! - Math.floor(Date.now() / 1000)).toBeGreaterThanOrEqual(59);
    expect((await f.rpc(token, `${kind}.get`, { path: { ...path, spaceID: "another-space" } })).status).toBe(400);
  }
  const readOnly = await f.appToken(["notes.read", "drawings.read"]);
  expect((await f.rpc(readOnly, "notes.list")).status).toBe(200);
  expect((await f.rpc(readOnly, "notes.create", { body: { title: "denied" } })).status).toBe(403);
  expect((await f.rpc(readOnly, "notes.collaboration.ticket", { path: { noteID: "missing" } })).status).toBe(403);
  expect((await f.rpc(token, "notes.list")).status).toBe(401);
  await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [f.spaceId, f.owner.user.id]);
  expect((await f.rpc(readOnly, "notes.list")).status).toBe(403);
});

it("keeps conversation-only documents and backlink sources hidden from nonparticipants", async () => {
  const f = await fixture(), conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.member.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES($1,$2,'person')", [conversation, f.member.user.id]);
  const target = await f.create("notes"), source = await f.create("notes", f.member.token), drawing = await f.create("drawings", f.member.token);
  await admin.query("UPDATE space_notes SET audience_kind='conversation',audience_conversation_id=$2 WHERE id=$1", [source.id, conversation]);
  await admin.query("UPDATE space_drawings SET audience_kind='conversation',audience_conversation_id=$2 WHERE id=$1", [drawing.id, conversation]);
  await admin.query("INSERT INTO space_note_links(source_note_id,target_note_id) VALUES($1,$2)", [source.id, target.id]);
  expect((await f.request(`${f.base}/notes/${source.id}`)).status).toBe(404);
  expect((await f.request(`${f.base}/drawings/${drawing.id}`)).status).toBe(404);
  expect((await f.request(`${f.base}/notes/${source.id}`, "DELETE")).status).toBe(404);
  expect((await (await f.request(`${f.base}/notes/${target.id}`)).json()).backlink_count).toBe(0);
  expect(await (await f.request(`${f.base}/notes/${target.id}/backlinks`)).json()).toEqual({ backlinks: [] });
  expect((await (await f.request(`${f.base}/notes/${target.id}/backlinks`, "GET", undefined, f.member.token)).json()).backlinks).toHaveLength(1);
  expect((await f.request(`${f.base}/notes/${source.id}/collaboration-ticket`, "POST", {}, f.member.token)).status).toBe(201);
});

it("rejects cross-Space mutations and member destruction without changing data or queuing controls", async () => {
  const f = await fixture(), note = await f.create("notes"), drawing = await f.create("drawings");
  for (const [kind, document] of [["notes", note], ["drawings", drawing]] as const) {
    expect((await f.request(`${f.base}/${kind}/${document.id}`, "DELETE", undefined, f.member.token)).status).toBe(404);
    expect((await f.request(`/spaces/wrong/${kind}/${document.id}`, "DELETE")).status).toBe(404);
    expect((await f.request(`/spaces/wrong/${kind}/${document.id}`, "PATCH", kind === "notes" ? { archived: true } : { title: "wrong" })).status).toBe(404);
  }
  expect((await admin.query("SELECT count(*) FROM space_note_control_outbox WHERE note_id=$1", [note.id])).rows[0].count).toBe("0");
  expect((await f.request(`${f.base}/notes/${note.id}`)).status).toBe(200);
});

it("archives/restores notes with atomic ACL invalidation and preserves idempotency", async () => {
  const f = await fixture(), note = await f.create("notes");
  for (const archived of [true, true, false]) expect((await f.request(`${f.base}/notes/${note.id}`, "PATCH", { archived })).status).toBe(204);
  const row = (await admin.query("SELECT lifecycle_state,acl_version FROM space_notes WHERE id=$1", [note.id])).rows[0];
  expect(row).toEqual({ lifecycle_state: "active", acl_version: "3" });
  expect((await admin.query("SELECT command,payload FROM space_note_control_outbox WHERE note_id=$1 ORDER BY created_at", [note.id])).rows).toEqual([
    { command: "acl", payload: { acl_version: 2 } }, { command: "acl", payload: { acl_version: 3 } }]);
});

it("rolls back document deletion when its required room-purge outbox cannot be written", async () => {
  const f = await fixture(), note = await f.create("notes");
  await admin.query("REVOKE INSERT ON space_note_control_outbox FROM misty_hono_app_test");
  try { expect((await f.request(`${f.base}/notes/${note.id}`, "DELETE")).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_note_control_outbox TO misty_hono_app_test"); }
  expect((await f.request(`${f.base}/notes/${note.id}`)).status).toBe(200);
  expect((await admin.query("SELECT count(*) FROM space_events WHERE entity_id=$1 AND event_type='note.deleted'", [note.id])).rows[0].count).toBe("0");
});

it("denies missing signing configuration after authorization and rejects invalid metadata without mutation", async () => {
  const f = await fixture(false), note = await f.create("notes");
  expect((await f.request(`${f.base}/notes/${note.id}/collaboration-ticket`, "POST", {})).status).toBe(503);
  expect((await f.request(`${f.base}/notes/${note.id}/collaboration-ticket`, "POST", {}, f.outsider.token)).status).toBe(404);
  for (const body of [{ shared_tags: ["🙂".repeat(21)] }, { shared_tags: Array(51).fill("tag") }, { shared_tags: [""] }, { title: "not CRDT" }])
    expect((await f.request(`${f.base}/notes/${note.id}/metadata`, "PATCH", body)).status).toBe(400);
  expect((await f.request(`${f.base}/drawings`, "POST", { title: "🙂".repeat(201) })).status).toBe(400);
});

it("applies signed Yjs projections and rebuilds backlinks only for newer revisions in the same Space", async () => {
  const f = await fixture(), source = await f.create("notes"), target = await f.create("notes");
  const input = { note_id: source.id, revision: 1, title: "  Synced  ", markdown: "# Synced", plain_text: "Synced",
    outgoing_note_ids: [target.id, target.id, source.id, "missing", " "] };
  expect(await (await f.project(input)).json()).toEqual({ applied: true });
  expect(await (await f.project({ ...input, title: "Stale" })).json()).toEqual({ applied: false });
  expect(await (await f.project({ ...input, revision: 2, title: "Rotated" }, { secret: collaborationConfig.previousProjectionSecret, prefix: "/api" })).json()).toEqual({ applied: true });
  const read = await (await f.request(`${f.base}/notes/${source.id}`)).json();
  expect(read).toMatchObject({ title: "Rotated", markdown: "# Synced", plain_text: "Synced", collaboration_revision: 2 });
  expect((await (await f.request(`${f.base}/notes/${target.id}/backlinks`)).json()).backlinks).toHaveLength(1);
  await f.request(`${f.base}/notes/${source.id}`, "PATCH", { archived: true });
  expect(await (await f.project({ ...input, revision: 3 })).json()).toEqual({ applied: false });
});

it("rejects bad, expired and swapped projection signatures and rolls back projection/link changes on event failure", async () => {
  const f = await fixture(), note = await f.create("notes"), target = await f.create("notes");
  const input = { note_id: note.id, revision: 1, title: "Projection", outgoing_note_ids: [target.id] };
  for (const options of [{ signature: "x".repeat(43) }, { age: 301 }, { age: -301 }, { secret: Buffer.alloc(32, 99) }])
    expect((await f.project(input, options)).status).toBe(401);
  expect((await f.project({ ...input, revision: 0 })).status).toBe(400);
  await admin.query("REVOKE INSERT ON space_events FROM misty_hono_app_test");
  try { expect((await f.project(input)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_hono_app_test"); }
  expect((await admin.query("SELECT title_projection,collaboration_revision FROM space_notes WHERE id=$1", [note.id])).rows[0]).toEqual({ title_projection: "Created", collaboration_revision: "0" });
  expect((await admin.query("SELECT count(*) FROM space_note_links WHERE source_note_id=$1", [note.id])).rows[0].count).toBe("0");
});

it("lists and unlinks assets with parent authorization, idempotency and delayed quota release", async () => {
  for (const kind of ["note", "drawing"] as const) {
    const f = await assetFixture(kind), upload = await f.reserve(), result = await (await f.finish(upload)).json();
    const asset = result[`${kind}_asset`], base = `${f.base}/${kind}s/${f.parent.id}/assets`;
    expect((await (await f.request(base)).json()).assets).toMatchObject([{ id: asset.id, byte_size: 2048, mime_type: "image/png" }]);
    expect((await f.request(base, "GET", undefined, f.outsider.token)).status).toBe(403);
    expect((await f.request(`${base}/${asset.id}`, "DELETE", undefined, f.member.token)).status).toBe(204);
    const removed = (await admin.query(`SELECT lifecycle_state,deleted_at FROM space_${kind}_assets WHERE id=$1`, [asset.id])).rows[0];
    expect(removed.lifecycle_state).toBe("unreferenced");
    expect((await f.request(`${base}/${asset.id}`, "DELETE")).status).toBe(204);
    expect((await admin.query(`SELECT deleted_at FROM space_${kind}_assets WHERE id=$1`, [asset.id])).rows[0].deleted_at).toEqual(removed.deleted_at);
    expect((await (await f.request(base)).json()).assets).toEqual([]);
    await createAssetRetention(application).runOnce();
    expect((await admin.query("SELECT used_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0].used_bytes).toBe("2048");
  }
});

it("retains shared blobs and held assets while releasing each logical contribution exactly once", async () => {
  const f = await assetFixture("note"), one = await f.reserve(), first = (await (await f.finish(one)).json()).note_asset;
  const two = await f.reserve(), second = (await (await f.finish(two)).json()).note_asset;
  const blob = (await admin.query("SELECT blob_id FROM library_files WHERE id=$1", [first.file_id])).rows[0].blob_id;
  const remove = async (id: string) => {
    expect((await f.request(`${f.base}/notes/${f.parent.id}/assets/${id}`, "DELETE")).status).toBe(204);
    await admin.query("UPDATE space_note_assets SET deleted_at=now()-interval '25 hours' WHERE id=$1", [id]);
  };
  const retention = createAssetRetention(application);
  await remove(first.id); await Promise.all([retention.runOnce(), retention.runOnce()]);
  expect((await admin.query("SELECT used_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0].used_bytes).toBe("2048");
  expect((await admin.query("SELECT lifecycle_state FROM library_blobs WHERE id=$1", [blob])).rows[0].lifecycle_state).toBe("ready");
  await remove(second.id);
  const hold = randomUUID();
  await admin.query(`INSERT INTO library_legal_holds(id,security_domain_id,space_id,target_kind,target_id,reason_code)
    SELECT $1,security_domain_id,id,'note_asset',$3,'test' FROM spaces WHERE id=$2`, [hold, f.spaceId, second.id]);
  await retention.runOnce();
  expect((await admin.query("SELECT used_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0].used_bytes).toBe("2048");
  await admin.query("UPDATE library_legal_holds SET active=false,released_at=now() WHERE id=$1", [hold]);
  await retention.runOnce(); await retention.runOnce();
  expect((await admin.query("SELECT used_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0].used_bytes).toBe("0");
  expect((await admin.query("SELECT lifecycle_state FROM library_blobs WHERE id=$1", [blob])).rows[0].lifecycle_state).toBe("deleted");
  expect((await admin.query("SELECT not_before>now()+interval '29 minutes' AS delayed FROM object_deletion_jobs WHERE object_key=$1", [one.key])).rows[0].delayed).toBe(true);
});

it("requires room purge, asset cleanup and released upload reservations before deleting document metadata", async () => {
  const f = await assetFixture("drawing"), upload = await f.reserve(); await f.finish(upload);
  const pending = await f.reserve();
  expect((await f.request(`${f.base}/drawings/${f.parent.id}`, "DELETE")).status).toBe(204);
  const retention = createDocumentRetention(application), exists = async () => (await admin.query("SELECT 1 FROM space_drawings WHERE id=$1", [f.parent.id])).rowCount;
  await retention.runOnce(); expect(await exists()).toBe(1);
  await createControlJobs(application, async () => {}).runOnce();
  await retention.runOnce(); expect(await exists()).toBe(1);
  // Reproduce the old Go deletion backlog: a deleting parent with ready assets.
  await admin.query("UPDATE space_drawing_assets SET lifecycle_state='ready',deleted_at=NULL WHERE drawing_id=$1", [f.parent.id]);
  await admin.query("UPDATE space_drawings SET updated_at=now()-interval '25 hours' WHERE id=$1", [f.parent.id]);
  await createAssetRetention(application).runOnce();
  await retention.runOnce(); expect(await exists()).toBe(1);
  await admin.query("UPDATE space_library_uploads SET expires_at=now()-interval '2 minutes' WHERE id=$1", [pending.upload.id]);
  await createStorageJobs(application, f.store).runOnce();
  await retention.runOnce(); expect(await exists()).toBe(0);
  expect((await admin.query("SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1", [f.spaceId])).rows[0]).toEqual({ used_bytes: "0", reserved_bytes: "0" });
});

it("fences room-control acknowledgements after reclaim and keeps delivery failures retryable", async () => {
  const f = await fixture(), note = await f.create("notes");
  await f.request(`${f.base}/notes/${note.id}`, "DELETE");
  let start!: () => void, finish!: () => void;
  const started = new Promise<void>((resolve) => { start = resolve; });
  const gate = new Promise<void>((resolve) => { finish = resolve; });
  const old = createControlJobs(application, async () => { start(); await gate; }).runOnce();
  await started;
  await admin.query("UPDATE space_note_control_outbox SET next_attempt_at=now()-interval '1 second' WHERE note_id=$1", [note.id]);
  await createControlJobs(application, async () => { throw new Error("sensitive upstream text"); }).runOnce();
  finish(); await old;
  const row = (await admin.query("SELECT attempts,delivered_at,last_error,next_attempt_at>now() AS delayed FROM space_note_control_outbox WHERE note_id=$1", [note.id])).rows[0];
  expect(row).toEqual({ attempts: 2, delivered_at: null, last_error: "control_delivery_failed", delayed: true });
  await admin.query("UPDATE space_note_control_outbox SET next_attempt_at=now()-interval '1 second' WHERE note_id=$1", [note.id]);
  let deliveries = 0; const jobs = createControlJobs(application, async () => { deliveries++; });
  await Promise.all([jobs.runOnce(), jobs.runOnce()]); expect(deliveries).toBe(1);
  expect((await admin.query("SELECT delivered_at,last_error FROM space_note_control_outbox WHERE note_id=$1", [note.id])).rows[0]).toMatchObject({ delivered_at: expect.any(Date), last_error: "" });
});

it("defers held document purge and its assets without blocking other room commands", async () => {
  const f = await assetFixture("note"), upload = await f.reserve(); await f.finish(upload);
  await f.request(`${f.base}/notes/${f.parent.id}`, "DELETE");
  await admin.query("UPDATE space_note_assets SET deleted_at=now()-interval '25 hours' WHERE note_id=$1", [f.parent.id]);
  const hold = randomUUID();
  await admin.query(`INSERT INTO library_legal_holds(id,security_domain_id,space_id,target_kind,target_id,reason_code)
    SELECT $1,security_domain_id,id,'note',$3,'test' FROM spaces WHERE id=$2`, [hold, f.spaceId, f.parent.id]);
  const other = await f.create("drawings"); await f.request(`${f.base}/drawings/${other.id}`, "DELETE");
  const delivered: string[] = [], controls = createControlJobs(application, async (_kind, id) => { delivered.push(id); });
  await controls.runOnce(); await createAssetRetention(application).runOnce();
  expect(delivered).toEqual([other.id]);
  expect((await admin.query("SELECT lifecycle_state FROM space_note_assets WHERE note_id=$1", [f.parent.id])).rows[0].lifecycle_state).toBe("deleting");
  await admin.query("UPDATE library_legal_holds SET active=false WHERE id=$1", [hold]);
  await controls.runOnce(); await createAssetRetention(application).runOnce();
  expect(delivered).toEqual([other.id, f.parent.id]);
  expect((await admin.query("SELECT lifecycle_state FROM space_note_assets WHERE note_id=$1", [f.parent.id])).rows[0].lifecycle_state).toBe("deleted");
});
