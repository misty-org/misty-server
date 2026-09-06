import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { assertRuntimeDatabaseRole } from "../../../../../../packages/database/src/roles.js";
import { createRequestBoundary } from "../../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../../app.js";
import { createAuthRepository } from "../../auth/repository.js";
import { createAuthService, hashToken } from "../../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../../auth/passwords.js";
import { createAccountDeletionJobs } from "./jobs.js";
import { createAccountDeletion } from "./repository.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_account_deletion_test') THEN CREATE ROLE misty_account_deletion_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_account_deletion_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,account_deletion_requests,account_deletion_steps,
      app_runtime_sessions,password_reset_tokens,auth_handoff_tokens,connection_authorization_requests,connected_account_oauth_states,
      cloud_oauth_states,provider_oauth_states,cloud_credential_handoffs,github_credential_handoffs,github_app_setup_states,password_recovery_jobs,
      trusted_devices,device_pairs,device_pairing_sessions,device_presence,self_host_accounts,self_host_enrollment_invitations,
      personal_agents,space_runs,agent_run_jobs,agent_run_tool_approvals,agent_run_contexts,workflow_device_node_jobs,
      space_agents,space_workflows,space_notes,space_drawings,space_note_control_outbox,space_drawing_control_outbox TO misty_account_deletion_test;
    GRANT SELECT,INSERT,UPDATE ON ai_user_settings,ai_surface_preferences,ai_recaps,ai_invocations,ai_invocation_contexts,ai_artifacts TO misty_account_deletion_test;
    GRANT SELECT,UPDATE ON spaces TO misty_account_deletion_test;
    GRANT SELECT ON space_members,space_integrations,space_provider_credentials,provider_shared_resources TO misty_account_deletion_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_account_deletion_test", max: 5, connectionTimeoutMillis: 5000 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM account_deletion_requests WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM personal_agents WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM password_recovery_jobs WHERE email IN (SELECT LOWER(email) FROM users WHERE id=ANY($1::text[]))", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0; spaces.length = 0; domains.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const register = async () => { const name = `delete_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username: name, email: `${name}@example.invalid`, name, password: "test-password", analyticsEnabled: false }); users.push(result.user.id); return result; };
  const account = await register(), boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const makeApi = (overrides: Partial<Parameters<typeof createAccountDeletion>[0]> = {}) => {
    const repository = createAccountDeletion({ pool: application, passwords, deployment: "hosted", ...overrides });
    return createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
      auth: { service: auth, deployment: "hosted", boundary }, accountDeletion: { auth, boundary, repository } });
  };
  const api = makeApi();
  const post = (prefix = "", body: unknown = { password: "test-password", confirmation: "DELETE" }, token = account.token) => api.request(`${prefix}/me/deletion`, { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(body) });
  const status = (id: string, token: string, prefix = "") => api.request(`${prefix}/account/deletion/status`, { method: "POST", body: JSON.stringify({ request_id: id, status_token: token }) });
  return { ...account, register, auth, api, post, status, makeApi };
}
async function space(userId: string, members: string[] = []) {
  const id = randomUUID(), domain = randomUUID(); spaces.push(id); domains.push(domain);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, userId, id]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Deletion Space',$3)", [id, userId, domain]);
    for (const member of new Set([userId, ...members])) await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,$3)", [id, member, member === userId ? "owner" : "member"]);
  }); return id;
}
async function assertActive(userId: string) {
  expect((await admin.query("SELECT lifecycle_state FROM users WHERE id=$1", [userId])).rows[0].lifecycle_state).toBe("active");
  expect((await admin.query("SELECT id FROM account_deletion_requests WHERE user_id=$1", [userId])).rowCount).toBe(0);
  expect((await admin.query("SELECT token_hash FROM sessions WHERE user_id=$1", [userId])).rowCount).toBeGreaterThan(0);
}
it("requires exact confirmation and password, rejects App/cross-origin credentials and shares alias rate limits", async () => {
  const f = await fixture(); await assertRuntimeDatabaseRole(application, "api");
  const appToken = randomUUID(); await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'journal','1.1.0')", [f.user.id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,expires_at) VALUES($1,$2,'journal',now()+INTERVAL '5 minutes')", [hashToken(appToken), f.user.id]);
  for (const prefix of ["", "/api", "/v1"]) expect((await f.post(prefix, undefined, appToken)).status).toBe(401);
  const cookie = await f.api.request("/me/deletion", { method: "POST", headers: { Cookie: `misty_session=${f.token}`, Origin: "https://untrusted.invalid" }, body: '{"password":"test-password","confirmation":"DELETE"}' }); expect(cookie.status).toBe(403);
  expect(await (await f.post("", { password: "test-password", confirmation: "delete" })).json()).toEqual({ code: "account_deletion_confirmation_required" });
  for (const prefix of ["", "/api", "/v1", ""]) expect(await (await f.post(prefix, { password: "wrong", confirmation: "DELETE" })).json()).toEqual({ code: "account_reauthentication_failed" });
  const limited = await f.post("/api"); expect(limited.status).toBe(429); expect(limited.headers.get("Retry-After")).toBeTruthy();
  await assertActive(f.user.id);
});
it("blocks owners of active shared Spaces and leaves the account untouched", async () => {
  const f = await fixture(), other = await f.register(), id = await space(f.user.id, [other.user.id]);
  const response = await f.post(); expect(response.status, await response.clone().text()).toBe(409);
  expect(await response.json()).toEqual({ code: "account_deletion_space_ownership", message: "Transfer or delete every Space you own before deleting your account.", spaces: [{ space_id: id, name: "Deletion Space", member_count: 2 }] });
  await assertActive(f.user.id);
  await admin.query("UPDATE spaces SET lifecycle_state='pending_deletion',deletion_requested_at=now() WHERE id=$1", [id]);
  const accepted = await f.post(); expect(accepted.status, await accepted.clone().text()).toBe(202);
});
it("atomically records four native cleanup steps and a hashed status capability while revoking every session", async () => {
  const f = await fixture(); await space(f.user.id);
  const extra = randomUUID(); await admin.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 day')", [hashToken(extra), f.user.id]);
  const response = await f.post("/v1"); expect(response.status, await response.clone().text()).toBe(202); expect(response.headers.get("Cache-Control")).toBe("no-store");
  const body = await response.json(); expect(body.request).toMatchObject({ status: "processing", provider_revocation_status: {} });
  expect(Object.keys(body.request).sort()).toEqual(["id", "status", "purge_after", "provider_revocation_status", "created_at", "updated_at"].sort());
  expect(Date.parse(body.request.purge_after) - Date.parse(body.request.created_at)).toBe(30 * 86400000);
  const row = (await admin.query("SELECT * FROM account_deletion_requests WHERE id=$1", [body.request.id])).rows[0]; expect(row.cleanup_owner).toBe("native"); expect(row.status_token_hash).toBe(hashToken(body.status_token)); expect(JSON.stringify(row)).not.toContain(body.status_token);
  const steps = (await admin.query("SELECT step,state,attempts,lease_token FROM account_deletion_steps WHERE request_id=$1 ORDER BY step", [body.request.id])).rows;
  expect(steps).toEqual(["local", "payments", "providers", "purge"].map(step => ({ step, state: "pending", attempts: 0, lease_token: null })));
  expect(await f.auth.authenticate(f.token)).toBeNull(); expect(await f.auth.authenticate(extra)).toBeNull(); expect((await f.post()).status).toBe(401);
  for (const prefix of ["", "/api", "/v1"]) { const result = await f.status(` ${body.request.id} `, ` ${body.status_token} `, prefix); expect(result.status).toBe(200); expect(await result.json()).toEqual(body.request); }
  const wrong = await f.status(body.request.id, extra); expect(wrong.status).toBe(404); expect(await wrong.json()).toEqual({ code: "account_deletion_not_found" });
  await withTransaction(application, async tx => { expect((await tx.query("SELECT request_id FROM account_deletion_steps WHERE request_id=$1", [body.request.id])).rowCount).toBe(0); }, { mode: "user", userId: f.user.id });
});
it("skips only the hosted payments stage for a self-hosted account", async () => {
  const f = await fixture();
  await admin.query("INSERT INTO self_host_accounts(user_id,entitlement_subject,entitlement_expires_at,is_admin) VALUES($1,'deletion-test-subject',now()+interval '1 day',true)", [f.user.id]);
  await admin.query("INSERT INTO self_host_enrollment_invitations(id,token_hash,created_by,expires_at) VALUES($1,$2,$3,now()+interval '1 day')", [`enrollment_${randomUUID()}`, hashToken(randomUUID()), f.user.id]);
  const response = await f.makeApi({ deployment: "self_hosted" }).request("/me/deletion", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"test-password","confirmation":"DELETE"}' });
  expect(response.status, await response.clone().text()).toBe(202); const body = await response.json();
  expect((await admin.query("SELECT state,result,completed_at IS NOT NULL AS completed FROM account_deletion_steps WHERE request_id=$1 AND step='payments'", [body.request.id])).rows[0]).toEqual({ state: "completed", result: { outcome: "not_applicable_self_hosted" }, completed: true });
  expect((await admin.query("SELECT count(*)::integer AS count FROM account_deletion_steps WHERE request_id=$1 AND state='pending'", [body.request.id])).rows[0].count).toBe(3);
  expect((await admin.query("SELECT disabled_at FROM self_host_accounts WHERE user_id=$1", [f.user.id])).rows[0].disabled_at).toBeTruthy();
  expect((await admin.query("SELECT revoked_at FROM self_host_enrollment_invitations WHERE created_by=$1", [f.user.id])).rows[0].revoked_at).toBeTruthy();
});
it("rechecks a changed password and a revoked session after asynchronous password verification", async () => {
  const f = await fixture();
  const changing: PasswordHasher = { ...passwords, verify: async (value, hash) => { const valid = await passwords.verify(value, hash); await admin.query("UPDATE users SET password_hash=$2 WHERE id=$1", [f.user.id, await passwords.hash("new-password")]); return valid; } };
  const send = (api: ReturnType<typeof createApi>, password: string) => api.request("/me/deletion", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: JSON.stringify({ password, confirmation: "DELETE" }) });
  expect(await (await send(f.makeApi({ passwords: changing }), "test-password")).json()).toEqual({ code: "account_reauthentication_failed" }); await assertActive(f.user.id);
  const revoking: PasswordHasher = { ...passwords, verify: async (value, hash) => { const valid = await passwords.verify(value, hash); await f.auth.logout(f.token); return valid; } };
  expect((await send(f.makeApi({ passwords: revoking }), "new-password")).status).toBe(401);
  expect((await admin.query("SELECT id FROM account_deletion_requests WHERE user_id=$1", [f.user.id])).rowCount).toBe(0);
});
it("rolls back access revocation and queue creation if the exact session expires during the transaction", async () => {
  const f = await fixture();
  // A test-only trigger expires the retained session deadline before the final
  // check; no hooks are added to production deletion code for this race.
  await admin.query(`CREATE FUNCTION misty_test_deletion_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='${f.user.id}' AND NEW.lifecycle_state='pending_deletion' THEN PERFORM pg_sleep(0.8); END IF; RETURN NEW; END $$;
    CREATE TRIGGER misty_test_deletion_delay BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION misty_test_deletion_delay();`);
  try {
    const shortened: PasswordHasher = { ...passwords, verify: async (value, hash) => { const valid = await passwords.verify(value, hash); await admin.query("UPDATE sessions SET expires_at=clock_timestamp()+interval '0.6 seconds' WHERE token_hash=$1", [hashToken(f.token)]); return valid; } };
    const response = await f.makeApi({ passwords: shortened }).request("/me/deletion", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"test-password","confirmation":"DELETE"}' });
    expect(response.status, await response.clone().text()).toBe(401); await assertActive(f.user.id);
  } finally { await admin.query("DROP TRIGGER misty_test_deletion_delay ON users; DROP FUNCTION misty_test_deletion_delay()"); }
});
it("returns a retryable failure for Space contention without partially deleting the account", async () => {
  const f = await fixture(), id = await space(f.user.id), lock = await admin.connect();
  try {
    await lock.query("BEGIN"); await lock.query("SELECT id FROM spaces WHERE id=$1 FOR UPDATE", [id]);
    const response = await f.post(); expect(response.status, await response.clone().text()).toBe(503); expect(await response.json()).toEqual({ code: "account_deletion_unavailable" });
  } finally { await lock.query("ROLLBACK"); lock.release(); }
  await assertActive(f.user.id);
});
it("supports existing Go status capabilities without adopting their cleanup jobs", async () => {
  const f = await fixture(), id = `deletion_${randomUUID()}`, token = randomUUID();
  await admin.query("INSERT INTO account_deletion_requests(id,user_id,status_token_hash,status,purge_after,last_error_code) VALUES($1,$2,$3,'scheduled',now()+interval '30 days','provider_unavailable')", [id, f.user.id, hashToken(token)]);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.user.id]);
  const response = await f.status(id, token); expect(response.status).toBe(200); expect(await response.json()).toMatchObject({ id, status: "scheduled", last_error_code: "provider_unavailable" });
  expect((await admin.query("SELECT cleanup_owner FROM account_deletion_requests WHERE id=$1", [id])).rows[0].cleanup_owner).toBe("go");
  expect((await admin.query("SELECT * FROM account_deletion_steps WHERE request_id=$1", [id])).rowCount).toBe(0);
});
async function work(spaceId: string, userId: string) {
  const id = randomUUID(), deviceId = `device_${randomUUID()}`;
  await admin.query("INSERT INTO trusted_devices(id,user_id,name,public_key) VALUES($1,$2,'Deletion device',$3)", [deviceId, userId, randomUUID()]);
  await admin.query(`INSERT INTO space_runs(id,space_id,resource_kind,resource_id,agent_id,initiated_by_user_id,billing_user_id,requesting_member_id,trigger_kind,state,approval_state,device_wait_hook_token,device_wait_expires_at)
    VALUES($1,$2,'agent','test-agent','test-agent',$3,$3,$3,'manual','awaiting_device','pending','old-hook',now()+interval '1 day')`, [id, spaceId, userId]);
  await admin.query("INSERT INTO agent_run_jobs(run_id,space_id,agent_id,state,lease_owner,lease_expires_at) VALUES($1,$2,'test-agent','leased','old-worker',now()+interval '1 minute')", [id, spaceId]);
  await admin.query(`INSERT INTO agent_run_tool_approvals(id,run_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token)
    VALUES($1,$1,$2,'call','tool','routine','hash','signed','hook')`, [id, userId]);
  await admin.query(`INSERT INTO agent_run_contexts(id,run_id,owner_user_id,space_id,device_id,kind,opaque_ref,expires_at)
    VALUES($1,$1,$2,$3,$4,'browser_tab','private-tab',now()+interval '1 day')`, [id, userId, spaceId, deviceId]);
  await admin.query(`INSERT INTO workflow_device_node_jobs(id,run_id,node_id,attempt,user_id,scope_id,operation,input,config,state,leased_device_id,lease_token_hash,lease_expires_at,context_id)
    VALUES($1,$1,'node',1,$2,'scope','operation','{}','{}','leased',$3,'old-token',now()+interval '1 minute',$1)`, [id, userId, deviceId]);
  return { id, deviceId };
}
it("revokes authorization grants and device access while retaining encrypted provider cleanup material", async () => {
  const f = await fixture(), other = await f.register(), id = await space(other.user.id, [f.user.id]), target = f.user.id;
  const connection = randomUUID();
  await admin.query("INSERT INTO cloud_connections(id,user_id,provider,name,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,'drive','Drive','private-account',$3,$4)", [connection, target, Buffer.from("encrypted-cloud"), Buffer.from("nonce")]);
  await admin.query("INSERT INTO connected_accounts(id,user_id,provider,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,'google','private-account',$3,$4)", [`connection_${randomUUID()}`, target, Buffer.from("encrypted-connected"), Buffer.from("nonce")]);
  const appToken = randomUUID(); await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'journal','1.1.0')", [target]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,expires_at) VALUES($1,$2,'journal',now()+interval '5 minutes')", [hashToken(appToken), target]);
  await admin.query("INSERT INTO password_reset_tokens(user_id,hashed_token,expires_at) VALUES($1,$2,now()+interval '1 hour')", [target, hashToken(randomUUID())]);
  await admin.query("INSERT INTO auth_handoff_tokens(hashed_token,user_id,redirect_path,expires_at) VALUES($1,$2,'/',now()+interval '1 hour')", [hashToken(randomUUID()), target]);
  await admin.query("INSERT INTO password_recovery_jobs(id,email,token_key_id,state,lease_owner,lease_expires_at,expires_at) VALUES($1,$2,'test-key','processing',$3,now()+interval '1 minute',now()+interval '1 hour')", [randomUUID(), f.user.email.toLowerCase(), randomUUID()]);
  await admin.query("INSERT INTO cloud_credential_handoffs(handoff_hash,user_id,cloud_connection_id,expires_at) VALUES($1,$2,$3,now()+interval '1 minute')", [hashToken(randomUUID()), target, connection]);
  await admin.query("INSERT INTO github_app_setup_states(state_hash,user_id,space_id,expires_at) VALUES($1,$2,$3,now()+interval '1 minute')", [hashToken(randomUUID()), target, id]);
  for (const table of ["connected_account_oauth_states", "connection_authorization_requests"] as const) {
    await admin.query(`INSERT INTO ${table}(state_hash,user_id,provider,capabilities,requested_scopes,verifier_ciphertext,verifier_nonce,expires_at${table === "connection_authorization_requests" ? ",actor,credential_snapshot,redirect_uri,client_id_hash" : ""})
      VALUES($1,$2,'google','[]','[]',$3,$4,now()+interval '1 minute'${table === "connection_authorization_requests" ? ",'{}','[]','https://api.example.invalid/callback',$1" : ""})`, [hashToken(randomUUID()), target, Buffer.from("cipher"), Buffer.from("nonce")]);
  }
  await admin.query("INSERT INTO cloud_oauth_states(state_hash,user_id,provider,connection_name,secret_ciphertext,secret_nonce,expires_at) VALUES($1,$2,'drive','Drive',$3,$4,now()+interval '1 minute')", [hashToken(randomUUID()), target, Buffer.from("cipher"), Buffer.from("nonce")]);
  await admin.query("INSERT INTO provider_oauth_states(state_hash,user_id,space_id,provider,verifier_ciphertext,verifier_nonce,expires_at) VALUES($1,$2,$3,'google',$4,$5,now()+interval '1 minute')", [hashToken(randomUUID()), target, id, Buffer.from("cipher"), Buffer.from("nonce")]);
  const own = await work(id, target), unaffected = await work(id, other.user.id);
  // A run charged to this user must be canceled even when owned and requested
  // by somebody else. It must lose approvals/context/device jobs as well.
  const charged = await work(id, other.user.id); await admin.query("UPDATE space_runs SET billing_user_id=$2 WHERE id=$1", [charged.id, target]);
  const devices = [own.deviceId, `device_${randomUUID()}`].sort();
  await admin.query("INSERT INTO trusted_devices(id,user_id,name,public_key) VALUES($1,$2,'Paired device',$3)", [devices.find(value => value !== own.deviceId), target, randomUUID()]);
  await admin.query("INSERT INTO device_pairs(id,owner_user_id,first_device_id,second_device_id) VALUES($1,$2,$3,$4)", [`pair_${randomUUID()}`, target, ...devices]);
  await admin.query("INSERT INTO device_pairing_sessions(id,owner_user_id,creator_device_id,qr_secret_hash,manual_code_hash,expires_at) VALUES($1,$2,$3,$4,$4,now()+interval '1 minute')", [`pairing_${randomUUID()}`, target, own.deviceId, hashToken(randomUUID())]);
  await admin.query("INSERT INTO device_presence(device_id,owner_user_id,p2p_endpoint_id,protocol_version) VALUES($1,$2,$3,'misty-device/1')", [own.deviceId, target, "a".repeat(32)]);
  const agent = randomUUID(); await admin.query("INSERT INTO personal_agents(id,owner_user_id,name,model_id) VALUES($1,$2,'Private agent','test-model')", [agent, target]);
  await admin.query("INSERT INTO space_agents(id,space_id,creator_user_id,name,schedules_enabled) VALUES($1,$2,$3,'Scheduled',true)", [randomUUID(), id, target]);
  await admin.query("INSERT INTO space_workflows(id,space_id,creator_user_id,name,stable_identifier,schedules_enabled) VALUES($1,$2,$3,'Scheduled',$1,true)", [randomUUID(), id, target]);
  for (const kind of ["note", "drawing"] as const) await admin.query(`INSERT INTO space_${kind}s(id,space_id,creator_user_id) VALUES($1,$2,$3)`, [randomUUID(), id, target]);
  const response = await f.post(); expect(response.status, await response.clone().text()).toBe(202);
  for (const table of ["sessions", "app_runtime_sessions", "password_reset_tokens", "auth_handoff_tokens", "connection_authorization_requests", "connected_account_oauth_states", "cloud_oauth_states", "provider_oauth_states", "cloud_credential_handoffs", "github_app_setup_states"]) expect((await admin.query(`SELECT * FROM ${table} WHERE user_id=$1`, [target])).rowCount, table).toBe(0);
  expect((await admin.query("SELECT state,lease_owner,lease_expires_at FROM password_recovery_jobs WHERE email=$1", [f.user.email.toLowerCase()])).rows[0]).toEqual({ state: "superseded", lease_owner: null, lease_expires_at: null });
  for (const table of ["device_presence", "device_pairing_sessions"]) expect((await admin.query(`SELECT * FROM ${table} WHERE owner_user_id=$1`, [target])).rowCount).toBe(0);
  expect((await admin.query("SELECT id FROM trusted_devices WHERE user_id=$1 AND revoked_at IS NULL", [target])).rowCount).toBe(0);
  expect((await admin.query("SELECT state FROM device_pairs WHERE owner_user_id=$1", [target])).rows[0].state).toBe("revoked");
  for (const run of [own.id, charged.id]) {
    expect((await admin.query("SELECT state,approval_state,device_wait_hook_token,device_wait_expires_at FROM space_runs WHERE id=$1", [run])).rows[0]).toEqual({ state: "canceled", approval_state: "denied", device_wait_hook_token: "", device_wait_expires_at: null });
    expect((await admin.query("SELECT state,lease_owner,lease_expires_at FROM agent_run_jobs WHERE run_id=$1", [run])).rows[0]).toEqual({ state: "canceled", lease_owner: null, lease_expires_at: null });
    expect((await admin.query("SELECT state FROM agent_run_tool_approvals WHERE run_id=$1", [run])).rows[0].state).toBe("denied");
    expect((await admin.query("SELECT state FROM agent_run_contexts WHERE run_id=$1", [run])).rows[0].state).toBe("detached");
    expect((await admin.query("SELECT state,lease_token_hash FROM workflow_device_node_jobs WHERE run_id=$1", [run])).rows[0]).toEqual({ state: "canceled", lease_token_hash: null });
  }
  expect((await admin.query("SELECT state FROM space_runs WHERE id=$1", [unaffected.id])).rows[0].state).toBe("awaiting_device"); expect(await f.auth.authenticate(other.token)).toBeTruthy();
  expect((await admin.query("SELECT enabled FROM personal_agents WHERE id=$1", [agent])).rows[0].enabled).toBe(false);
  for (const table of ["space_agents", "space_workflows"]) expect((await admin.query(`SELECT schedules_enabled FROM ${table} WHERE creator_user_id=$1`, [target])).rows[0].schedules_enabled).toBe(false);
  for (const kind of ["note", "drawing"] as const) {
    const row = (await admin.query(`SELECT id,acl_version FROM space_${kind}s WHERE space_id=$1`, [id])).rows[0]; expect(Number(row.acl_version)).toBe(2);
    expect((await admin.query(`SELECT command,payload FROM space_${kind}_control_outbox WHERE ${kind}_id=$1`, [row.id])).rows[0]).toEqual({ command: "acl", payload: { acl_version: 2 } });
  }
  for (const table of ["cloud_connections", "connected_accounts"]) { const row = (await admin.query(`SELECT credential_ciphertext,revoked_at FROM ${table} WHERE user_id=$1`, [target])).rows[0]; expect(row.credential_ciphertext.length).toBeGreaterThan(0); expect(row.revoked_at).toBeNull(); }
  // Shared membership/content remains for the fenced local cleanup stage.
  expect((await admin.query("SELECT * FROM space_members WHERE user_id=$1", [target])).rowCount).toBe(1);
});
it("bounds simultaneous password work across aliases and does not duplicate concurrent deletion requests", async () => {
  const f = await fixture(); let entered = 0;
  const deferred = () => { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done; }); return { promise, resolve }; };
  const ready = deferred(), release = deferred();
  const delayed: PasswordHasher = { ...passwords, verify: async (value, hash) => { const valid = await passwords.verify(value, hash); if (++entered === 2) ready.resolve(); await release.promise; return valid; } };
  const api = f.makeApi({ passwords: delayed });
  const send = (prefix: string) => api.request(`${prefix}/me/deletion`, { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{"password":"test-password","confirmation":"DELETE"}' });
  const first = send(""), second = send("/api");
  try {
    await ready.promise; const excess = await send("/v1"); expect(excess.status).toBe(503); expect(await excess.json()).toEqual({ code: "account_deletion_unavailable" });
  } finally { release.resolve(); }
  const results = await Promise.all([first, second]); expect(results.map(row => row.status).sort()).toEqual([202, 409]);
  expect((await admin.query("SELECT id FROM account_deletion_requests WHERE user_id=$1", [f.user.id])).rowCount).toBe(1);
  expect((await admin.query("SELECT * FROM account_deletion_steps WHERE request_id IN (SELECT id FROM account_deletion_requests WHERE user_id=$1)", [f.user.id])).rowCount).toBe(4);
});
it("orders durable cleanup dependencies and never purges before the retention deadline", async () => {
  const f = await fixture(), response = await f.post(); expect(response.status, await response.clone().text()).toBe(202);
  const id = (await response.json()).request.id, jobs = createAccountDeletionJobs(application);
  expect(await jobs.claim("local")).toBeNull(); expect(await jobs.claim("purge")).toBeNull();
  const claims = await Promise.all([jobs.claim("payments"), jobs.claim("payments")]);
  expect(claims.filter(Boolean)).toHaveLength(1); expect(claims.find(Boolean)).toMatchObject({ request_id: id, step: "payments", attempts: 1, user_id: f.user.id, license_id: f.user.license_id });
  expect(await jobs.claim("providers")).toMatchObject({ request_id: id, step: "providers", attempts: 1 });
  // Fixtures represent handler acknowledgements. Claim/retry cannot acknowledge
  // cleanup themselves, and no handlers are enabled in the production entrypoint.
  await admin.query("UPDATE account_deletion_steps SET state='completed',completed_at=now(),lease_token=NULL,lease_expires_at=NULL WHERE request_id=$1 AND step IN ('payments','providers')", [id]);
  const local = await jobs.claim("local"); expect(local).toMatchObject({ request_id: id, step: "local" }); expect(await jobs.claim("purge")).toBeNull();
  await admin.query("UPDATE account_deletion_steps SET state='completed',completed_at=now(),lease_token=NULL,lease_expires_at=NULL WHERE request_id=$1 AND step='local'", [id]);
  await admin.query("UPDATE account_deletion_requests SET status='scheduled' WHERE id=$1", [id]);
  expect(await jobs.retry(local!)).toBe(false); expect(await jobs.claim("purge")).toBeNull();
  await admin.query("UPDATE account_deletion_requests SET purge_after=now()-interval '1 minute',cleanup_owner='go' WHERE id=$1", [id]); expect(await jobs.claim("purge")).toBeNull();
  await admin.query("UPDATE account_deletion_requests SET cleanup_owner='native' WHERE id=$1", [id]);
  await admin.query("UPDATE users SET lifecycle_state='deleted' WHERE id=$1", [f.user.id]); expect(await jobs.claim("purge")).toBeNull();
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.user.id]); expect(await jobs.claim("purge")).toMatchObject({ request_id: id, step: "purge", attempts: 1 });
});
it("fences stale cleanup workers and applies bounded retries without discarding the request", async () => {
  const f = await fixture(), response = await f.post(); expect(response.status, await response.clone().text()).toBe(202);
  const id = (await response.json()).request.id, jobs = createAccountDeletionJobs(application), first = (await jobs.claim("payments"))!;
  expect(await jobs.retry(first)).toBe(true); expect(await jobs.claim("payments")).toBeNull();
  const deferred = (await admin.query("SELECT state,lease_token,last_error_code,extract(epoch FROM available_at-clock_timestamp()) AS delay FROM account_deletion_steps WHERE request_id=$1 AND step='payments'", [id])).rows[0];
  expect(deferred).toMatchObject({ state: "pending", lease_token: null, last_error_code: "payments_cleanup_unavailable" }); expect(Number(deferred.delay)).toBeGreaterThan(10); expect(Number(deferred.delay)).toBeLessThanOrEqual(15);
  await admin.query("UPDATE account_deletion_steps SET available_at=now()-interval '1 minute' WHERE request_id=$1 AND step='payments'", [id]);
  const second = (await jobs.claim("payments"))!; expect(second.attempts).toBe(2); expect(second.lease_token).not.toBe(first.lease_token); expect(await jobs.retry(first)).toBe(false);
  await admin.query("UPDATE account_deletion_steps SET lease_expires_at=now()-interval '1 minute' WHERE request_id=$1 AND step='payments'", [id]);
  expect(await jobs.retry(second)).toBe(false); const third = (await jobs.claim("payments"))!; expect(third.attempts).toBe(3); expect(third.lease_token).not.toBe(second.lease_token);
  expect(await jobs.retry(second)).toBe(false);
  expect((await admin.query("SELECT lifecycle_state FROM users WHERE id=$1", [f.user.id])).rows[0].lifecycle_state).toBe("pending_deletion");
  expect((await admin.query("SELECT status,last_error_code FROM account_deletion_requests WHERE id=$1", [id])).rows[0]).toEqual({ status: "processing", last_error_code: "payments_cleanup_unavailable" });
});
it("bounds status bodies and rate limits across aliases without trusting caller IP headers", async () => {
  const f = await fixture();
  const oversized = await f.api.request("/account/deletion/status", { method: "POST", body: JSON.stringify({ request_id: "a".repeat(5000), status_token: "wrong" }) }); expect(oversized.status).toBe(400);
  for (let count = 0; count < 59; count++) expect((await f.status("unknown", "wrong", ["", "/api", "/v1"][count % 3]!)).status).toBe(404);
  const limited = await f.api.request("/v1/account/deletion/status", { method: "POST", headers: { "X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8" }, body: '{"request_id":"unknown","status_token":"wrong"}' });
  expect(limited.status).toBe(429); expect(limited.headers.get("Retry-After")).toBeTruthy(); expect(limited.headers.get("Cache-Control")).toBe("no-store");
});
it("cancels AI admission and schedules while retaining unresolved remote effects and other accounts", async () => {
  const f = await fixture(), other = await f.register(), ids: string[] = [];
  for (const user of [f.user.id, other.user.id]) {
    await admin.query("INSERT INTO ai_user_settings(user_id) VALUES($1)", [user]);
    await admin.query("INSERT INTO ai_surface_preferences(user_id,surface_id,proactive_enabled) VALUES($1,'home',true)", [user]);
    await admin.query("INSERT INTO ai_recaps(user_id,surface_id,enabled,state,next_run_at,lease_until,last_result) VALUES($1,'home',true,'running',now(),now()+interval '10 minutes','retained evidence')", [user]);
    for (const state of ['queued','running','awaiting_approval','completed']) {
      const id = randomUUID(); ids.push(id);
      await admin.query(`INSERT INTO ai_invocations(id,user_id,surface_id,mode,trigger_kind,state,idempotency_key,runtime_kind,runtime_run_id,request_payload)
        VALUES($1,$2,'home','quick','message',$3,$1,'agent-runtime','retained-runtime-'||$1,'{"private":"reconciliation"}')`, [id,user,state]);
      if(state === 'running') for(const artifactState of ['proposed','applying']) await admin.query(`INSERT INTO ai_artifacts(id,invocation_id,user_id,schema_version,kind,title,risk,approval_policy,idempotency_key,state,expires_at)
        VALUES($1,$2,$3,1,'mail_draft','Evidence','consequential','confirm',$1,$4,now()+interval '1 day')`, [randomUUID(),id,user,artifactState]);
    }
  }
  const response = await f.post(); expect(response.status,await response.clone().text()).toBe(202);
  expect((await admin.query("SELECT enabled,cursor_companion_enabled,memory_enabled FROM ai_user_settings WHERE user_id=$1", [f.user.id])).rows[0]).toEqual({enabled:false,cursor_companion_enabled:false,memory_enabled:false});
  const own = (await admin.query("SELECT state,error_code,runtime_run_id,request_payload FROM ai_invocations WHERE id=ANY($1::text[]) ORDER BY created_at,id", [ids.slice(0,4)])).rows;
  expect(own.filter(r=>r.state==='canceled')).toHaveLength(3); expect(own.filter(r=>r.state==='completed')).toHaveLength(1);
  for(const row of own) { expect(row.runtime_run_id).toMatch(/^retained-runtime-/); expect(row.request_payload).toEqual({private:'reconciliation'}); }
  const recap = (await admin.query("SELECT enabled,state,next_run_at,lease_until,last_result FROM ai_recaps WHERE user_id=$1", [f.user.id])).rows[0];
  expect(recap).toMatchObject({enabled:false,state:'failed',next_run_at:null,last_result:'retained evidence'}); expect(recap.lease_until.getTime()).toBeGreaterThan(Date.now());
  expect((await admin.query("SELECT state FROM ai_artifacts WHERE user_id=$1 ORDER BY state", [f.user.id])).rows).toEqual([{state:'applying'},{state:'rejected'}]);
  expect((await admin.query("SELECT enabled,state FROM ai_recaps WHERE user_id=$1", [other.user.id])).rows[0]).toEqual({enabled:true,state:'running'});
  expect((await admin.query("SELECT 1 FROM ai_recaps WHERE user_id=$1 AND enabled AND next_run_at<=now()", [f.user.id])).rowCount).toBe(0);
});
