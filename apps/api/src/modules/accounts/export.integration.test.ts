import { generateKeyPairSync, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { jwtVerify } from "jose";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { assertRuntimeDatabaseRole } from "../../../../../packages/database/src/roles.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createCollaborationTickets } from "../collaboration/tickets.js";
import type { ObjectStore } from "../storage/object-store.js";
import { createAccountRepository } from "./repository.js";
import { createAccountExport } from "./export.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool, passwords: PasswordHasher;
const keys = generateKeyPairSync("ed25519"), config = { origin: "https://collaboration.example.invalid", privateKey: keys.privateKey,
  roomSalt: Buffer.alloc(32, 10), controlSecret: Buffer.alloc(32, 11), projectionSecret: Buffer.alloc(32, 12), previousProjectionSecret: null, issuer: "misty-api", audience: "misty-journal-collab" };
const signer = createCollaborationTickets(config);
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_account_export_test') THEN CREATE ROLE misty_account_export_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_account_export_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions TO misty_account_export_test;
    GRANT SELECT,UPDATE ON spaces,space_notes,space_drawings,space_messages,space_note_assets,space_drawing_assets,space_library_items,space_message_attachments,library_files,library_blobs TO misty_account_export_test;
    GRANT SELECT ON space_members,space_conversations,space_conversation_members,personal_agents,personal_agent_versions,cloud_connections TO misty_account_export_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_account_export_test", max: 5 }); passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM library_files WHERE security_domain_id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM library_blobs WHERE security_domain_id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM personal_agents WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); spaces.length = 0; domains.length = 0; users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const register = async () => { const name = `export_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username: name, email: `${name}@example.invalid`, name, password: "test-password", analyticsEnabled: false }); users.push(result.user.id); return result; };
  const account = await register();
  const signDownload = vi.fn(async (_key: string, filename: string, expires: Date) => ({ url: `https://objects.example.invalid/signed/${randomUUID()}`, filename, expires_at: expires.toISOString() }));
  const store: ObjectStore = { head: async () => null, delete: async () => {}, signUpload: async () => { throw new Error("Unexpected upload"); }, signDownload };
  const makeApi = (overrides: Partial<Parameters<typeof createAccountExport>[0]> = {}) => createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, deployment: "hosted", boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }) },
    accounts: { auth, repository: createAccountRepository(application), export: createAccountExport({ pool: application, passwords, tickets: signer, store, ...overrides }) } });
  const api = makeApi();
  const post = (prefix = "", password = "test-password", token = account.token) => api.request(`${prefix}/me/export`, { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify({ password }) });
  return { ...account, auth, register, api, post, makeApi, signDownload, store };
}
async function space(userId: string, members: string[] = []) {
  const id = randomUUID(), domain = randomUUID(); spaces.push(id); domains.push(domain);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, userId, id]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Export Space',$3)", [id, userId, domain]);
    for (const member of new Set([userId, ...members])) await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,$3)", [id, member, member === userId ? "owner" : "member"]);
  }); return id;
}
async function document(userId: string, spaceId: string, kind: "note" | "drawing" = "note") {
  const id = randomUUID();
  await admin.query(`INSERT INTO space_${kind}s(id,space_id,creator_user_id,${kind === "note" ? "title_projection" : "title"}) VALUES($1,$2,$3,'Portable document')`, [id, spaceId, userId]); return id;
}
async function file(userId: string, spaceId: string) {
  const id = randomUUID(), blob = randomUUID(), key = `library/${randomUUID()}`;
  const domain = (await admin.query("SELECT security_domain_id FROM spaces WHERE id=$1", [spaceId])).rows[0].security_domain_id;
  await admin.query("INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,server_detected_mime_type,lifecycle_state) VALUES($1,$2,$3,$4,32,'image/png','ready')", [blob, domain, key, randomUUID().replaceAll("-", "").repeat(2)]);
  await admin.query("INSERT INTO library_files(id,blob_id,security_domain_id,uploader_user_id,original_filename,lifecycle_state) VALUES($1,$2,$3,$4,'portable.png','ready')", [id, blob, domain, userId]); return { id, key };
}
async function asset(userId: string, spaceId: string, parent: string, kind: "note" | "drawing" = "note") {
  const object = await file(userId, spaceId), id = randomUUID();
  await admin.query(`INSERT INTO space_${kind}_assets(id,${kind}_id,file_id,uploader_user_id,display_name${kind === "drawing" ? ",excalidraw_file_id" : ""}) VALUES($1,$2,$3,$4,'portable.png'${kind === "drawing" ? ",$1" : ""})`, [id, parent, object.id, userId]); return { ...object, assetId: id };
}
it("preserves the empty format and aliases while denying App credentials and incorrect reauthentication", async () => {
  const f = await fixture(); await assertRuntimeDatabaseRole(application, "api");
  const token = randomUUID(); await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'journal','1.1.0')", [f.user.id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,expires_at) VALUES($1,$2,'journal',now()+INTERVAL '5 minutes')", [hashToken(token), f.user.id]);
  for (const prefix of ["", "/api", "/v1"]) {
    expect((await f.post(prefix, "test-password", token)).status).toBe(401);
    const response = await f.post(prefix); expect(response.status, await response.clone().text()).toBe(200); expect(response.headers.get("Cache-Control")).toBe("no-store");
    expect(await response.json()).toMatchObject({ account_data: { format_version: 2, account: { id: f.user.id }, settings: { analytics_enabled: false }, spaces: [], journal: null, assets: null, authored_messages: [], agents: [], cloud_connections: [] }, documents: [], assets: [] });
  }
  const wrong = await f.post("", "wrong"); expect(wrong.status).toBe(401); expect(await wrong.json()).toEqual({ code: "account_reauthentication_failed" });
  const cookie = await f.api.request("/me/export", { method: "POST", headers: { Cookie: `misty_session=${f.token}`, Origin: "https://untrusted.invalid" }, body: '{"password":"test-password"}' }); expect(cookie.status).toBe(403);
});
it("exports owned data, versions and signed viewer/asset descriptors without secrets or numeric precision loss", async () => {
  const f = await fixture(), other = await f.register(), spaceId = await space(f.user.id, [other.user.id]);
  const note = await document(f.user.id, spaceId), drawing = await document(f.user.id, spaceId, "drawing");
  await asset(f.user.id, spaceId, note); await asset(other.user.id, spaceId, drawing, "drawing"); await document(other.user.id, spaceId);
  await admin.query(`INSERT INTO space_messages(id,space_id,sender_user_id,content) VALUES($1,$2,$3,'[{"type":"text","text":"portable","exact":9007199254740993}]')`, [randomUUID(), spaceId, f.user.id]);
  const agent = randomUUID(); await admin.query("INSERT INTO personal_agents(id,owner_user_id,name,instructions,avatar,model_id) VALUES($1,$2,'Portable agent','own instructions','{\"kind\":\"preset\",\"custom\":null}','test-model')", [agent, f.user.id]);
  await admin.query("INSERT INTO personal_agent_versions(id,agent_id,version,name,instructions,model_mode,checksum_sha256,created_by_user_id) VALUES($1,$2,1,'Version one','historic instructions','automatic',$3,$4)", [randomUUID(), agent, "a".repeat(64), f.user.id]);
  await admin.query("INSERT INTO cloud_connections(id,user_id,provider,name,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,'drive','Drive','visible-account',$3,$4)", [randomUUID(), f.user.id, Buffer.from("secret ciphertext"), Buffer.from("secret nonce")]);
  const response = await f.post(); expect(response.status, await response.clone().text()).toBe(200); const raw = await response.text(), body = JSON.parse(raw);
  expect(raw).toContain("9007199254740993"); for (const secret of ["password_hash", "credential_ciphertext", "credential_nonce", "object_key", "secret ciphertext", f.token]) expect(raw).not.toContain(secret);
  expect(body.account_data.authored_messages).toHaveLength(1); expect(body.account_data.agents).toHaveLength(1);
  expect(body.account_data.agents[0]).toMatchObject({ id: agent, instructions: "own instructions", avatar: { custom: null }, versions: [{ instructions: "historic instructions" }], space_memberships: [] });
  expect(body.account_data.agents[0].model_id).toBe("test-model"); expect(body.account_data.agents[0].reasoning_effort).toBeUndefined(); expect(body.account_data.agents[0].deleted_at).toBeUndefined();
  expect(body.documents.map((row: {id: string}) => row.id).sort()).toEqual([note, drawing].sort());
  for (const row of body.documents) {
    const url = new URL(row.download_url); expect(url.protocol).toBe("https:"); expect(url.searchParams.get("export")).toBe("1");
    const claims = (await jwtVerify(url.searchParams.get("ticket")!, keys.publicKey, { issuer: "misty-api", audience: "misty-journal-collab" })).payload;
    expect(claims).toMatchObject({ sub: f.user.id, role: "viewer", resource_id: row.id, space_id: spaceId, acl_version: row.acl_version }); expect(claims.exp! - Date.now()/1000).toBeGreaterThan(890);
  }
  expect(body.assets).toHaveLength(2); expect(body.assets[0]).toMatchObject({ byte_size: 32, mime_type: "image/png", download: { byte_size: 32, mime_type: "image/png", filename: "portable.png" } }); expect(f.signDownload).toHaveBeenCalledTimes(2);
});
it("withholds private, archived and former-Space documents and their assets", async () => {
  const f = await fixture(), other = await f.register(), id = await space(other.user.id, [f.user.id]);
  const own = await document(f.user.id, id), hidden = await document(f.user.id, id), archived = await document(f.user.id, id);
  await asset(f.user.id, id, own); await asset(f.user.id, id, hidden); await asset(f.user.id, id, archived);
  const conversation = randomUUID(); await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, id, other.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES($1,$2,'person')", [conversation, other.user.id]);
  await admin.query("UPDATE space_notes SET audience_kind='conversation',audience_conversation_id=$2 WHERE id=$1", [hidden, conversation]);
  await admin.query("UPDATE space_notes SET lifecycle_state='archived',archived_at=now() WHERE id=$1", [archived]);
  const first = await (await f.post()).json(); expect(first.documents.map((row: {id: string}) => row.id)).toEqual([own]); expect(first.assets).toHaveLength(1);
  await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [id, f.user.id]);
  const former = await (await f.post()).json(); expect(former.documents).toEqual([]); expect(former.assets).toEqual([]); expect(former.account_data.spaces).toEqual([]);
});
it("fails complete exports when signing is unavailable and retains authorization locks through signing", async () => {
  const f = await fixture(), id = await space(f.user.id), note = await document(f.user.id, id); await asset(f.user.id, id, note);
  const post = (api: ReturnType<typeof createApi>) => api.request("/me/export", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"test-password"}' });
  expect((await post(f.makeApi({ tickets: null }))).status).toBe(503);
  const missing = await post(f.makeApi({ store: null })); expect(missing.status).toBe(503); expect(await missing.json()).toEqual({ code: "account_export_assets_unavailable" });
  f.signDownload.mockImplementationOnce(async (_key, filename, expires) => {
    for (const query of ["SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT", "SELECT token_hash FROM sessions WHERE user_id=$1 FOR UPDATE NOWAIT"]) await expect(admin.query(query, [f.user.id])).rejects.toMatchObject({ code: "55P03" });
    await expect(admin.query("SELECT id FROM space_notes WHERE id=$1 FOR UPDATE NOWAIT", [note])).rejects.toMatchObject({ code: "55P03" });
    return { url: "https://objects.example.invalid/signed", filename, expires_at: expires.toISOString() };
  });
  const response = await f.post(); expect(response.status, await response.clone().text()).toBe(200); await response.body!.cancel();
});
it("rechecks password and session after asynchronous password work before exporting content", async () => {
  const f = await fixture(); let changed = false;
  const passwordsWithRace: PasswordHasher = { ...passwords, verify: async (value, hash) => { const valid = await passwords.verify(value, hash); if (!changed) { changed = true; await admin.query("UPDATE users SET password_hash=$2 WHERE id=$1", [f.user.id, await passwords.hash("new-password")]); } return valid; } };
  const response = await f.makeApi({ passwords: passwordsWithRace }).request("/me/export", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"test-password"}' });
  expect(response.status).toBe(401); expect(await response.json()).toEqual({ code: "account_reauthentication_failed" });
  const revoke: PasswordHasher = { ...passwords, verify: async (value, hash) => { const valid = await passwords.verify(value, hash); await f.auth.logout(f.token); return valid; } };
  expect((await f.makeApi({ passwords: revoke }).request("/me/export", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"new-password"}' })).status).toBe(401);
});
it("includes authorized Library and message attachments while excluding private conversations and retired files", async () => {
  const f = await fixture(), other = await f.register(), spaceId = await space(f.user.id, [other.user.id]);
  const library = await file(f.user.id, spaceId), libraryId = randomUUID();
  await admin.query("INSERT INTO space_library_items(id,space_id,file_id,contributing_user_id,display_name,added_by_user_id) VALUES($1,$2,$3,$4,'Library.png',$4)", [libraryId, spaceId, library.id, f.user.id]);
  const blob = await file(f.user.id, spaceId), message = randomUUID(), attachment = randomUUID(), upload = randomUUID();
  await admin.query("INSERT INTO space_messages(id,space_id,sender_user_id) VALUES($1,$2,$3)", [message, spaceId, f.user.id]);
  await admin.query(`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,requested_byte_size,client_sha256,state,upload_token_hash,expires_at)
    SELECT $1,id,security_domain_id,$3,$1,'attached.png','attachment',32,$4,'ready',$1,now()+INTERVAL '1 hour' FROM spaces WHERE id=$2`, [upload, spaceId, f.user.id, "b".repeat(64)]);
  await admin.query("INSERT INTO space_message_attachments(id,space_id,message_id,file_id,upload_id,uploader_user_id,display_name) VALUES($1,$2,$3,$4,$5,$6,'Attached.png')", [attachment, spaceId, message, blob.id, upload, f.user.id]);
  const body = await (await f.post()).json(); expect(body.assets.map((row: {kind: string}) => row.kind).sort()).toEqual(["library", "message_attachment"]);
  expect(body.assets.find((row: {kind: string}) => row.kind === "message_attachment").parent_id).toBe(message);
  const conversation = randomUUID(); await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, spaceId, other.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES($1,$2,'person')", [conversation, other.user.id]);
  await admin.query("UPDATE space_messages SET conversation_id=$2 WHERE id=$1", [message, conversation]);
  await admin.query("UPDATE library_files SET lifecycle_state='trash' WHERE id=$1", [library.id]);
  const denied = await (await f.post()).json(); expect(denied.assets).toEqual([]); expect(denied.account_data.authored_messages).toEqual([]);
});
it("streams a multi-megabyte history without dropping rows across cursor fetches", async () => {
  const f = await fixture(), spaceId = await space(f.user.id);
  await admin.query(`INSERT INTO space_messages(id,space_id,sender_user_id,content)
    SELECT $1||'-'||n,$2,$3,jsonb_build_array(jsonb_build_object('type','text','text',repeat('x',10000),'row',n)) FROM generate_series(1,250) n`, [randomUUID(), spaceId, f.user.id]);
  const response = await f.post(); expect(response.status).toBe(200); expect(Number(response.headers.get("Content-Length"))).toBeGreaterThan(2500000);
  const body = await response.json(); expect(body.account_data.authored_messages).toHaveLength(250);
  expect(new Set(body.account_data.authored_messages.map((row: {content: {row: number}[]}) => row.content[0]!.row)).size).toBe(250);
});
it("shares export admission across aliases, rolls back signing failures and releases canceled downloads", async () => {
  const f = await fixture(), id = await space(f.user.id), note = await document(f.user.id, id); await asset(f.user.id, id, note);
  let entered = 0, ready!: () => void, release!: () => void;
  const started = new Promise<void>(resolve => { ready = resolve; }), gate = new Promise<void>(resolve => { release = resolve; });
  f.signDownload.mockImplementation(async () => { if (++entered === 2) ready(); await gate; throw new Error("Signing unavailable after earlier export data"); });
  const pending = [f.post(), f.post("/api")];
  try {
    await Promise.race([started, Promise.all(pending).then(responses => { throw new Error(`Export ended before gate: ${responses.map(r => r.status)}`); })]);
    expect((await f.post("/v1")).status).toBe(503);
  } finally { release(); }
  for (const response of await Promise.all(pending)) { expect(response.status).toBe(500); expect(await response.text()).not.toContain("account_data"); }
  f.signDownload.mockImplementation(async (_key, filename, expires) => ({ url: "https://objects.example.invalid/signed", filename, expires_at: expires.toISOString() }));
  const response = await f.post(); expect(response.status).toBe(200); await response.body!.cancel();
  const next = await f.post("/v1"); expect(next.status).toBe(200); await next.body!.cancel();
});
it("rejects a session that expires during signing before publishing any manifest", async () => {
  const f = await fixture(), id = await space(f.user.id), note = await document(f.user.id, id); await asset(f.user.id, id, note);
  await admin.query("UPDATE sessions SET expires_at=now()+INTERVAL '1 second' WHERE token_hash=$1", [hashToken(f.token)]);
  f.signDownload.mockImplementationOnce(async (_key, filename, expires) => { await new Promise(resolve => setTimeout(resolve, 1100)); return { url: "https://objects.example.invalid/signed", filename, expires_at: expires.toISOString() }; });
  const response = await f.post(); expect(response.status).toBe(401); expect(await response.text()).not.toContain("account_data");
});
it("preserves the original cursor lock timeout and returns 503 without consuming export admission", async () => {
  const f = await fixture(), id = await space(f.user.id), tx = await admin.connect();
  try {
    await tx.query("BEGIN"); await tx.query("SELECT id FROM spaces WHERE id=$1 FOR UPDATE", [id]);
    const response = await f.post(); expect(response.status).toBe(503); expect(await response.json()).toEqual({ code: "account_export_unavailable" });
  } finally { await tx.query("ROLLBACK"); tx.release(); }
  const next = await f.post(); expect(next.status).toBe(200); await next.body!.cancel();
});
it("retains admission for canceled requests until their transaction has unwound", async () => {
  const f = await fixture(), id = await space(f.user.id), note = await document(f.user.id, id); await asset(f.user.id, id, note);
  let entered = 0, ready!: () => void, release!: () => void;
  const started = new Promise<void>(resolve => { ready = resolve; }), gate = new Promise<void>(resolve => { release = resolve; });
  f.signDownload.mockImplementation(async (_key, filename, expires) => { if (++entered === 2) ready(); await gate; return { url: "https://objects.example.invalid/signed", filename, expires_at: expires.toISOString() }; });
  const controller = new AbortController();
  const pending = ["", "/v1"].map(prefix => f.api.request(`${prefix}/me/export`, { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"test-password"}', signal: controller.signal }));
  try {
    await Promise.race([started, Promise.all(pending).then(responses => { throw new Error(`Export ended before gate: ${responses.map(r => r.status)}`); })]);
    controller.abort();
    expect((await f.post("/api")).status).toBe(503);
  } finally { release(); }
  expect((await Promise.all(pending)).map(response => response.status)).toEqual([503, 503]);
  const response = await f.post(); expect(response.status).toBe(200); await response.body!.cancel();
});
