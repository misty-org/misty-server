import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { parseMethodResult } from "@misty/contracts";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { AppSessionRevoked, createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createOfficialCatalog } from "../official-apps/catalog.js";
import { createInstallationRepository } from "../official-apps/repository.js";
import { createInvitationRepository } from "./invitations/repository.js";
import { createInvitationJobs } from "./invitations/jobs.js";
import { invitationToken } from "./invitations/config.js";
import { createOnboardingRepository } from "../onboarding/repository.js";
import { createSpaceRepository } from "./repository.js";
import { listTemplates } from "./templates.js";
const admin = createTestDatabase(), users: string[] = [];
let application: Pool, passwords: PasswordHasher;
const catalog = createOfficialCatalog();
const invitationConfig = { baseUrl: "https://invites.example.invalid/invite", keys: { active: "test", keys: new Map([["test", Buffer.alloc(32, 9)]]) } };
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,user_app_installations,app_runtime_sessions,app_personal_records,app_data_deletion_jobs,app_install_events,
      spaces,security_domains,space_members,space_roles,space_storage_usage,owner_storage_usage,space_setup_integrations,space_creation_requests,onboarding_completions,
      space_tasks,space_task_counters,space_albums,space_notes,space_note_control_outbox,space_events TO misty_hono_app_test;
    GRANT SELECT ON personal_agents,personal_agent_versions,space_runs,space_messages,space_member_permission_overrides,space_invitations,space_storage_contributions,space_upload_reservations,space_rendition_reservations TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON space_invitations,space_invitation_delivery_jobs TO misty_hono_app_test;
    GRANT INSERT,UPDATE,DELETE ON space_member_permission_overrides TO misty_hono_app_test;
    GRANT INSERT ON space_library_audit_events TO misty_hono_app_test;
    GRANT USAGE,SELECT ON space_library_audit_events_id_seq TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 10 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM onboarding_completions WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map((row) => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM personal_agents WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const spaces = createSpaceRepository(application), runtime = createAppRuntimeRepository(application), installations = createInstallationRepository(application);
  const boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary, deployment: "hosted" }, spaces: { auth, appRuntime: runtime, repository: spaces },
    invitations: { auth, repository: createInvitationRepository(application, invitationConfig) },
    onboarding: { auth, catalog, repository: createOnboardingRepository(application) }, officialApps: { auth, catalog, repository: installations }, appRuntime: { repository: runtime } });
  const username = `spaces_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const { user, token } = await auth.register({ username, email: `${username}@example.invalid`, name: "Space test", password: "test-password", analyticsEnabled: false }); users.push(user.id);
  const request = (path: string, method = "GET", body?: unknown, credential = token, headers: Record<string, string> = {}) => app.request(path, { method,
    headers: { Authorization: `Bearer ${credential}`, "Content-Type": "application/json", ...headers }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}) });
  const onboard = (name = "Personal Space", prefix = "/v1") => request(`${prefix}/onboarding/finish`, "POST", { space_name: name, app_ids: ["terminal", "invented"] });
  const create = async (name = "Created Space", template = "blank", providers: string[] = [], key = "") => {
    const response = await request("/v1/spaces", "POST", { name, template_id: template, integration_providers: providers }, token, key ? { "Idempotency-Key": key } : {});
    expect(response.status, await response.clone().text()).toBe(201); return response.json();
  };
  return { app, auth, user, token, request, onboard, create, spaces, runtime, installations };
}

it("atomically completes onboarding with reviewed defaults, protected Space and stable retries", async () => {
  const f = await fixture();
  const results = await Promise.all([f.onboard("  Personal Space  "), f.onboard("Personal Space", "/api")]);
  for (const result of results) expect(result.status, await result.clone().text()).toBe(201);
  const first = await results[0]!.json(); expect(await results[1]!.json()).toEqual(first);
  expect(() => parseMethodResult("spaces.get", first.space)).not.toThrow();
  expect(first.space).toMatchObject({ name: "Personal Space", is_default: true, role: "owner", member_count: 1, pending_count: 0, is_shared: false, permissions: { "space.delete": false, "space.transfer": false, "space.rename": true } });
  expect(first.apps.map((app: { app_id: string }) => app.app_id)).toEqual(["inbox", "chat", "journal", "files", "agents"]);
  for (const app of first.apps) expect(app.granted_scopes).toEqual(catalog.find(app.app_id)!.scopes);
  expect((await admin.query("SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1", [first.space.id])).rows[0]).toEqual({ used_bytes: "0", reserved_bytes: "0" });
  expect((await admin.query("SELECT count(*) FROM space_roles WHERE space_id=$1 AND is_everyone", [first.space.id])).rows[0].count).toBe("1");
  expect(await (await f.onboard("Different")).json()).toEqual({ code: "onboarding_request_changed" });
  const retry = await f.onboard("Personal Space", ""); expect(retry.status).toBe(201); expect(await retry.json()).toEqual(first);
});

it("rolls back Space, app grants and events if completion cannot commit", async () => {
  const f = await fixture(); await admin.query("REVOKE INSERT ON onboarding_completions FROM misty_hono_app_test");
  try { expect((await f.onboard()).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON onboarding_completions TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1", [f.user.id])).rows[0].count).toBe("0");
  expect((await f.installations.list(f.user.id))).toEqual([]);
  expect((await admin.query("SELECT count(*) FROM security_domains WHERE owner_user_id=$1 AND kind='space'", [f.user.id])).rows[0].count).toBe("0");
  expect((await f.onboard()).status).toBe(201);
});

it("serializes ordinary creation with onboarding and enforces owned-Space limits under contention", async () => {
  const f = await fixture(), created = await f.create(); expect(created.space.is_default).toBe(true);
  expect(await (await f.onboard()).json()).toEqual({ code: "onboarding_already_complete" });
  await f.create("Second");
  const responses = await Promise.all(Array.from({ length: 5 }, (_, index) => f.request("/spaces", "POST", { name: `Contended ${index}` })));
  expect(responses.filter((r) => r.status === 201)).toHaveLength(1);
  for (const response of responses.filter((r) => r.status !== 201)) expect(await response.json()).toEqual({ code: "space_ownership_limit_reached" });
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1", [f.user.id])).rows[0].count).toBe("3");
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1 AND is_default", [f.user.id])).rows[0].count).toBe("1");
  const race = await fixture();
  const [onboard, ordinary] = await Promise.all([race.onboard(), race.request("/spaces", "POST", { name: "Ordinary" })]);
  expect(ordinary.status).toBe(201); expect([201, 409]).toContain(onboard.status);
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1 AND is_default", [race.user.id])).rows[0].count).toBe("1");
});

it("creates every built-in template once with canonical providers and durable note bootstrap", async () => {
  const f = await fixture(); await admin.query("UPDATE licenses SET tier='pro' WHERE user_id=$1", [f.user.id]);
  for (const template of listTemplates()) {
    const key = `template-${template.id}`, created = await f.create(`Team <${template.id}>`, template.id, [" Notion ", "google", "notion"], key);
    const replay = await f.create(`Team <${template.id}>`, template.id, ["google", "notion"], key); expect(replay).toEqual(created);
    expect(created.setup).toEqual({ selected_providers: ["google", "notion"], completed_providers: [], pending_providers: ["google", "notion"] });
    const counts = (await admin.query(`SELECT (SELECT count(*) FROM space_tasks WHERE space_id=$1) AS tasks,
      (SELECT count(*) FROM space_notes WHERE space_id=$1) AS notes,(SELECT count(*) FROM space_albums WHERE space_id=$1) AS albums`, [created.space.id])).rows[0];
    expect(counts).toEqual({ tasks: String(template.seed_summary.task_count), notes: String(template.seed_summary.note_count), albums: String(template.seed_summary.collection_count) });
    if (template.seed_summary.note_count) expect((await admin.query("SELECT count(*) FROM space_note_control_outbox o JOIN space_notes n ON n.id=o.note_id WHERE n.space_id=$1 AND command='bootstrap'", [created.space.id])).rows[0].count).toBe("1");
  }
  expect((await f.request("/spaces", "POST", { name: "changed" }, f.token, { "Idempotency-Key": "template-blank" })).status).toBe(409);
});

it("authorizes native Space reads, owner rename/setup and scoped app RPC with current credentials", async () => {
  const f = await fixture(), member = await fixture(), outsider = await fixture(), created = await f.create("Space", "blank", ["google"]), id = created.space.id;
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [id, member.user.id]);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'messages.read','deny',$3),($1,$2,'tasks.view','deny',$3),($1,$2,'storage.manage','allow',$3)", [id, member.user.id, f.user.id]);
  const read = await (await member.request(`/spaces/${id}`)).json();
  expect(read.permissions).toMatchObject({ "messages.read": false, "messages.write": false, "attachments.upload": false, "tasks.view": false, "tasks.manage": false, "storage.manage": true, "space.rename": false });
  expect((await outsider.request(`/spaces/${id}`)).status).toBe(404);
  expect((await member.request(`/spaces/${id}`, "PATCH", { name: "denied" })).status).toBe(403);
  for (const method of ["PUT", "PATCH"]) expect((await f.request(`/api/spaces/${id}`, method, { name: "Renamed" })).status).toBe(200);
  expect((await member.request(`/spaces/${id}/setup`, "PATCH", { provider: "google", status: "skipped" })).status).toBe(403);
  expect(await (await f.request(`/spaces/${id}/setup`, "PATCH", { provider: "google", status: "skipped" })).json()).toEqual({ selected_providers: ["google"], completed_providers: ["google"], pending_providers: [] });
  const app = { ...catalog.find("terminal")!, scopes: ["spaces.read"] };
  await f.installations.install(f.user.id, app); const token = randomUUID(); await f.installations.session(f.user.id, app.id, hashToken(token), id);
  const rpc = () => f.request("/app-runtime/rpc", "POST", { protocol: 2, method: "spaces.get", params: {} }, token);
  const result = await rpc(); expect(result.status, await result.clone().text()).toBe(200);
  const value = await result.json(); expect(() => parseMethodResult("spaces.get", value)).not.toThrow();
  expect((await f.request("/spaces", "GET", undefined, token)).status).toBe(401);
  expect((await f.request(`/spaces/${id}`, "PATCH", { name: "app forbidden" }, token)).status).toBe(401);
  const session = await f.runtime.findSession(hashToken(token));
  await f.installations.uninstall(f.user.id, app.id); expect((await rpc()).status).toBe(401);
  await expect(f.spaces.get({ userId: f.user.id, appSession: session! }, id)).rejects.toBeInstanceOf(AppSessionRevoked);
});

it("lists account Spaces with plan, personal storage and only current invitations", async () => {
  const f = await fixture(), other = await fixture(), created = await other.create();
  await admin.query(`INSERT INTO space_invitations(id,space_id,invited_user_id,invited_email,invited_by_user_id,token_hash,expires_at)
    VALUES($1,$2,$3,$4,$5,$6,now()+interval '1 day')`, [randomUUID(), created.space.id, f.user.id, f.user.email, other.user.id, hashToken(randomUUID())]);
  for (const prefix of ["", "/api", "/v1"]) {
    const result = await (await f.request(`${prefix}/spaces`)).json();
    expect(result.spaces).toEqual([]); expect(result.invitations).toHaveLength(1);
    expect(result.entitlements).toMatchObject({ plan: "basic", max_owned_spaces: 3, personal_storage_limit_bytes: 2000000000 });
    expect(result.owner_storage).toMatchObject({ user_id: f.user.id, used_bytes: 0, reserved_bytes: 0, remaining_bytes: 2000000000, spaces: [] });
  }
  await admin.query("UPDATE space_invitations SET expires_at=now()-interval '1 second' WHERE invited_user_id=$1", [f.user.id]);
  expect((await (await f.request("/spaces")).json()).invitations).toEqual([]);
});

it("validates names and creation inputs before mutation and exposes the existing public template catalog", async () => {
  const f = await fixture();
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.app.request(`${prefix}/space-templates`); expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ templates: listTemplates(), providers: [{ provider: "github", configured: false }] });
  }
  for (const body of [{ name: "" }, { name: "🙂".repeat(81) }, { name: "Space", template_id: "unknown" }, { name: "Space", integration_providers: ["unknown"] }, { name: "Space", owner_user_id: "forged" }]) expect((await f.request("/spaces", "POST", body)).status).toBe(400);
  expect((await f.request("/spaces", "POST", { name: "Space" }, f.token, { "Idempotency-Key": "x".repeat(201) })).status).toBe(400);
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1", [f.user.id])).rows[0].count).toBe("0");
  const created = await f.create("\u0085" + "🙂".repeat(80) + "\u0085"); expect(created.space.name).toBe("🙂".repeat(80));
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.user.id]);
  expect((await f.onboard()).status).toBe(401);
  await expect(f.spaces.create(f.user.id, { name: "Retired", template_id: "blank", integration_providers: [] }, "")).rejects.toThrow("not_authenticated");
});

it("serves member and agent lists through native SDK RPC while protecting other owners' private agent fields", async () => {
  const f = await fixture(), member = await fixture(), outsider = await fixture(), created = await f.create(), spaceId = created.space.id;
  await admin.query("INSERT INTO space_members(space_id,user_id,role,read_message_seq) VALUES($1,$2,'member',7)", [spaceId, member.user.id]);
  async function agent(owner: string, name: string, enabled = true) {
    const id = randomUUID();
    await admin.query("INSERT INTO personal_agents(id,owner_user_id,name,enabled,model_id) VALUES($1,$2,$3,$4,'private-model')", [id, owner, name, enabled]);
    await admin.query(`INSERT INTO personal_agent_versions(id,agent_id,version,name,instructions,model_mode,model_id,reasoning_effort,checksum_sha256,created_by_user_id)
      VALUES($1,$2,1,$3,'private instructions','automatic','private-model','high',$4,$5)`, [randomUUID(), id, name, "a".repeat(64), owner]);
    return id;
  }
  const own = await agent(f.user.id, "Own"), shared = await agent(member.user.id, "Shared"), hidden = await agent(member.user.id, "Hidden");
  await agent(f.user.id, "Disabled hidden", false);
  const taskId = randomUUID();
  await admin.query(`INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,priority,rank,due_timezone,source_refs,created_by_user_id,assignee_agent_id)
    VALUES($1,$2,1,'MST-1','Shared task','todo','medium',1024,'UTC','[]',$3,$4)`, [taskId, spaceId, f.user.id, shared]);
  for (const [state, age] of [["awaiting_approval", 2], ["failed", 1]] as const) await admin.query(`INSERT INTO space_runs(id,space_id,resource_kind,resource_id,agent_id,initiated_by_user_id,billing_user_id,requesting_member_id,trigger_kind,state,source_task_id,updated_at)
    VALUES($1,$2,'agent',$3,$3,$4,$4,$4,'manual',$5,$6,now()-$7*interval '1 minute')`, [randomUUID(), spaceId, shared, member.user.id, state, taskId, age]);
  await f.installations.install(f.user.id, { ...catalog.find("terminal")!, scopes: ["spaces.read"] });
  const token = randomUUID(); await f.installations.session(f.user.id, "terminal", hashToken(token), spaceId);
  const response = await f.request("/v1/app-runtime/rpc", "POST", { protocol: 2, method: "spaces.members.list", params: {} }, token);
  expect(response.status, await response.clone().text()).toBe(200); const result = await response.json();
  expect(() => parseMethodResult("spaces.members.list", result)).not.toThrow();
  expect(result.members.map((item: { user_id: string }) => item.user_id)).toEqual([f.user.id, member.user.id]);
  expect(result.members[1].read_message_seq).toBe(7);
  expect(result.agents.map((item: { agent_id: string }) => item.agent_id)).toEqual([own, shared]);
  expect(result.agents[0]).toMatchObject({ instructions: "private instructions", model_id: "private-model", can_control: true, work_state: "ready" });
  expect(result.agents[1]).toMatchObject({ can_control: false, work_state: "awaiting_approval", attention_count: 2, current_task_id: taskId });
  for (const key of ["instructions", "model_id", "reasoning_effort", "permissions", "capability_grants", "role_permissions"]) expect(result.agents[1]).not.toHaveProperty(key);
  expect(JSON.stringify(result)).not.toContain(hidden);
  expect((await outsider.request(`/spaces/${spaceId}/members`)).status).toBe(403);
  expect((await f.request(`/spaces/${spaceId}/agents`)).status).toBe(200);
  await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [spaceId, f.user.id]);
  expect((await f.request("/app-runtime/rpc", "POST", { protocol: 2, method: "spaces.members.list", params: {} }, token)).status).toBe(403);
});

it("applies owner permission overrides and dependencies, supports inheritance and rejects unauthorized targets", async () => {
  const f = await fixture(), member = await fixture(), outsider = await fixture(), created = await f.create(), id = created.space.id;
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [id, member.user.id]);
  const path = `/spaces/${id}/members/${member.user.id}/permissions`;
  const defaults = await (await member.request(path)).json();
  expect(defaults.permissions).toMatchObject({ "messages.read": true, "messages.write": true, "attachments.upload": true, "storage.manage": false });
  expect(defaults.permissions).not.toHaveProperty("space.delete");
  expect((await member.request(`/spaces/${id}/members/${f.user.id}/permissions`)).status).toBe(403);
  expect((await outsider.request(path)).status).toBe(403);
  expect((await member.request(path, "PUT", { permission: "storage.manage", effect: "allow" })).status).toBe(403);
  const changed = await (await f.request(path, "PUT", { permission: "messages.read", effect: "deny" })).json();
  expect(changed.permissions).toMatchObject({ "messages.read": false, "messages.write": false, "attachments.upload": false });
  const inherited = await (await f.request(path, "PUT", { permission: "messages.read", effect: "inherit" })).json(); expect(inherited).toEqual(defaults);
  expect((await admin.query("SELECT count(*) FROM space_member_permission_overrides WHERE space_id=$1 AND user_id=$2", [id, member.user.id])).rows[0].count).toBe("0");
  expect((await f.request(`/spaces/${id}/members/${f.user.id}/permissions`, "PUT", { permission: "messages.read", effect: "deny" })).status).toBe(400);
  for (const body of [{ permission: "space.delete", effect: "allow" }, { permission: "messages.read", effect: "invalid" }]) expect((await f.request(path, "PUT", body)).status).toBe(400);
  expect((await f.request(`/spaces/${id}/members/missing/permissions`)).status).toBe(404);
  expect((await admin.query("SELECT count(*) FROM space_library_audit_events WHERE space_id=$1 AND action='space.permission.updated'", [id])).rows[0].count).toBe("2");
});

it("rolls back a permission change when its audit fails and rechecks ownership after removal", async () => {
  const f = await fixture(), member = await fixture(), created = await f.create(), id = created.space.id;
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [id, member.user.id]);
  const path = `/spaces/${id}/members/${member.user.id}/permissions`;
  await admin.query("REVOKE INSERT ON space_library_audit_events FROM misty_hono_app_test");
  try { expect((await f.request(path, "PUT", { permission: "messages.read", effect: "deny" })).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_library_audit_events TO misty_hono_app_test"); }
  expect((await (await member.request(path)).json()).permissions["messages.read"]).toBe(true);
  await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [id, f.user.id]);
  await expect(f.spaces.permissions({ userId: f.user.id }, id, member.user.id, { permission: "messages.read", effect: "deny" })).rejects.toThrow("forbidden");
});

async function invitationFixture() {
  const owner = await fixture(), recipient = await fixture(), outsider = await fixture(), created = await owner.create("Shared <Space>");
  const spaceId = created.space.id, path = `/spaces/${spaceId}/invitations`;
  const issue = async () => {
    const response = await owner.request(path, "POST", { email: ` ${recipient.user.email.toUpperCase()} ` });
    expect(response.status, await response.clone().text()).toBe(201); return response.json();
  };
  const tokenFor = async (id: string) => {
    const job = (await admin.query("SELECT j.generation,j.token_key_id,i.invited_email FROM space_invitation_delivery_jobs j JOIN space_invitations i ON i.id=j.invite_id WHERE invite_id=$1", [id])).rows[0];
    return invitationToken(invitationConfig.keys, job.token_key_id, id, job.generation, job.invited_email);
  };
  return { owner, recipient, outsider, spaceId, path, issue, tokenFor };
}

it("creates durable invitations, delivers bounded private links and joins the matching account once", async () => {
  const f = await invitationFixture(), invite = await f.issue(), token = await f.tokenFor(invite.id);
  expect(invite.delivery_status).toBe("pending"); expect(JSON.stringify(invite)).not.toContain(token); expect(invite).not.toHaveProperty("token_hash");
  const jobs = (await admin.query("SELECT * FROM space_invitation_delivery_jobs WHERE invite_id=$1", [invite.id])).rows[0]; expect(JSON.stringify(jobs)).not.toContain(token);
  const messages: { text: string; html: string; to: string }[] = [];
  await createInvitationJobs(application, invitationConfig, async (message) => { messages.push(message); }).runOnce();
  expect(messages).toHaveLength(1); expect(messages[0]!.to).toBe(f.recipient.user.email);
  expect(messages[0]!.text).toContain(`${invitationConfig.baseUrl}/${token}`); expect(messages[0]!.html).toContain("Shared &lt;Space&gt;");
  expect((await admin.query("SELECT delivery_status FROM space_invitations WHERE id=$1", [invite.id])).rows[0].delivery_status).toBe("sent");
  for (const prefix of ["", "/api", "/v1"]) {
    const preview = await f.owner.app.request(`${prefix}/space-invitations/${token}`);
    expect(preview.status).toBe(200); expect(preview.headers.get("Referrer-Policy")).toBe("no-referrer");
    expect(await preview.json()).toMatchObject({ space_name: "Shared <Space>", invited_email: f.recipient.user.email });
  }
  expect((await f.outsider.request(`/space-invitations/${token}`, "POST", { accept: true })).status).toBe(404);
  const results = await Promise.all([f.recipient.request(`/space-invitations/${token}`, "POST", { accept: true }), f.recipient.request(`/spaces/invitations/${invite.id}/accept`, "POST")]);
  expect(results.map((r) => r.status).sort()).toEqual([200, 404]);
  const accepted = await results.find((r) => r.status === 200)!.json(); expect(() => parseMethodResult("spaces.get", accepted)).not.toThrow();
  expect(accepted).toMatchObject({ id: f.spaceId, role: "member", member_count: 2 });
  expect((await f.owner.app.request(`/space-invitations/${token}`)).status).toBe(404);
  expect((await admin.query("SELECT count(*) FROM space_events WHERE space_id=$1 AND event_type='member.joined'", [f.spaceId])).rows[0].count).toBe("1");
});

it("invalidates old links on resend and fences an older delivery acknowledgement", async () => {
  const f = await invitationFixture(), invite = await f.issue(), oldToken = await f.tokenFor(invite.id);
  let start!: () => void, finish!: () => void;
  const started = new Promise<void>((resolve) => { start = resolve; }), gate = new Promise<void>((resolve) => { finish = resolve; });
  const running = createInvitationJobs(application, invitationConfig, async () => { start(); await gate; }).runOnce();
  await started;
  const resent = await f.owner.request(`${f.path}/${invite.id}/resend`, "POST"); expect(resent.status).toBe(200);
  const token = await f.tokenFor(invite.id); expect(token).not.toBe(oldToken);
  expect((await f.owner.app.request(`/space-invitations/${oldToken}`)).status).toBe(404);
  finish(); await running;
  expect((await admin.query("SELECT delivery_status FROM space_invitations WHERE id=$1", [invite.id])).rows[0].delivery_status).toBe("pending");
  expect((await admin.query("SELECT state,attempts FROM space_invitation_delivery_jobs WHERE invite_id=$1", [invite.id])).rows[0]).toEqual({ state: "pending", attempts: 0 });
  let calls = 0; const worker = createInvitationJobs(application, invitationConfig, async () => { calls++; });
  await Promise.all([worker.runOnce(), worker.runOnce()]); expect(calls).toBe(1);
  expect((await f.recipient.request(`/space-invitations/${token}`, "POST", { accept: false })).status).toBe(204);
  expect((await f.recipient.request(`/spaces/invitations/${invite.id}/accept`, "POST")).status).toBe(404);
});

it("retries failed delivery and suppresses revoked, consumed or expired invitations", async () => {
  const f = await invitationFixture(), invite = await f.issue();
  let sends = 0;
  const worker = createInvitationJobs(application, invitationConfig, async () => { sends++; throw new Error("upstream secret detail"); });
  await worker.runOnce(); await worker.runOnce(); expect(sends).toBe(1);
  expect((await admin.query("SELECT state,last_error,available_at>now() AS delayed FROM space_invitation_delivery_jobs WHERE invite_id=$1", [invite.id])).rows[0]).toEqual({ state: "pending", last_error: "invitation_delivery_failed", delayed: true });
  expect((await f.owner.request(`${f.path}/${invite.id}`, "DELETE")).status).toBe(204);
  await admin.query("UPDATE space_invitation_delivery_jobs SET available_at=now()-interval '1 second' WHERE invite_id=$1", [invite.id]);
  await worker.runOnce(); expect(sends).toBe(1);
  expect((await admin.query("SELECT state FROM space_invitation_delivery_jobs WHERE invite_id=$1", [invite.id])).rows[0].state).toBe("superseded");
  const expired = await f.issue(), token = await f.tokenFor(expired.id);
  await admin.query("UPDATE space_invitations SET expires_at=now()-interval '1 second' WHERE id=$1", [expired.id]);
  expect((await f.recipient.request(`/spaces/invitations/${expired.id}/accept`, "POST")).status).toBe(410);
  expect((await f.owner.app.request(`/space-invitations/${token}`)).status).toBe(404);
  await worker.runOnce(); expect(sends).toBe(1);
  expect((await admin.query("SELECT revoked_at FROM space_invitations WHERE id=$1", [expired.id])).rows[0].revoked_at).toBeInstanceOf(Date);
});

it("enforces invitation management boundaries and rolls back creation and joining on durable-write failure", async () => {
  const f = await invitationFixture();
  for (const method of ["GET", "POST"]) expect((await f.outsider.request(f.path, method, method === "POST" ? { email: f.recipient.user.email } : undefined)).status).toBe(403);
  await admin.query("REVOKE INSERT ON space_invitation_delivery_jobs FROM misty_hono_app_test");
  try { expect((await f.owner.request(f.path, "POST", { email: f.recipient.user.email })).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_invitation_delivery_jobs TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM space_invitations WHERE space_id=$1", [f.spaceId])).rows[0].count).toBe("0");
  const invite = await f.issue();
  await admin.query("REVOKE INSERT ON space_events FROM misty_hono_app_test");
  try { expect((await f.recipient.request(`/spaces/invitations/${invite.id}/accept`, "POST")).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM space_members WHERE space_id=$1 AND user_id=$2", [f.spaceId, f.recipient.user.id])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT consumed_at FROM space_invitations WHERE id=$1", [invite.id])).rows[0].consumed_at).toBeNull();
  expect((await f.recipient.request(`/spaces/invitations/${invite.id}/accept`, "POST")).status).toBe(200);
  expect((await f.owner.request(f.path, "POST", { email: f.recipient.user.email })).status).toBe(409);
});

it("supports signup after invitation and redeems legacy hashed links without native delivery keys", async () => {
  const f = await fixture(), created = await f.create(), path = `/spaces/${created.space.id}/invitations`, username = `invited_${randomUUID().replaceAll("-", "").slice(0, 12)}`, email = `${username}@example.invalid`;
  const response = await f.request(path, "POST", { email }); expect(response.status).toBe(201); const invite = await response.json(); expect(invite.invited_user_id).toBeNull();
  const signedUp = await f.auth.register({ username, email, name: "Invited later", password: "test-password", analyticsEnabled: false }); users.push(signedUp.user.id);
  const accepted = await f.request(`/spaces/invitations/${invite.id}/accept`, "POST", undefined, signedUp.token); expect(accepted.status).toBe(200);
  const legacyRecipient = await fixture(), legacyId = randomUUID(), legacyToken = `legacy-${randomUUID()}`;
  await admin.query(`INSERT INTO space_invitations(id,space_id,invited_user_id,invited_email,invited_by_user_id,token_hash,delivery_status,expires_at)
    VALUES($1,$2,$3,$4,$5,$6,'sent',now()+interval '1 day')`, [legacyId, created.space.id, legacyRecipient.user.id, legacyRecipient.user.email, f.user.id, hashToken(legacyToken)]);
  const repository = createInvitationRepository(application, null);
  expect(await repository.preview(legacyToken)).toMatchObject({ invited_email: legacyRecipient.user.email });
  expect(await repository.respond(legacyRecipient.user.id, { token: legacyToken }, true)).toMatchObject({ role: "member", id: created.space.id });
  await expect(repository.issue(f.user.id, created.space.id, "another@example.invalid")).rejects.toThrow("invitation_unavailable");
});
