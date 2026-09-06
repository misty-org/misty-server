import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createOfficialCatalog } from "../official-apps/catalog.js";
import { createInstallationRepository } from "../official-apps/repository.js";
import { createJournalNotes } from "../journal/notes.js";
import { createJournalDrawings } from "../journal/drawings.js";
import { requireJournalMember } from "../journal/access.js";
import { createUsageRepository } from "../usage/repository.js";
import { createSpaceRepository } from "./repository.js";

const admin = createTestDatabase(), users: string[] = [];
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,
      space_setup_integrations,space_creation_requests,space_notes,space_note_control_outbox,space_drawings,space_drawing_control_outbox,space_events,
      space_conversations,space_conversation_members,space_agents,space_workflows,space_runs,agent_run_jobs,agent_run_tool_approvals,agent_run_contexts,
      workflow_device_node_jobs,user_app_installations,app_runtime_sessions,app_install_events,app_data_deletion_jobs TO misty_hono_app_test;
    GRANT SELECT ON space_member_permission_overrides,space_invitations,space_note_links TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON hosted_ai_wallets,space_hosted_ai_wallets,hosted_ai_reservations,hosted_ai_usage_ledger TO misty_hono_app_test;
    GRANT SELECT ON space_upload_reservations,space_rendition_reservations TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 10 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map((row) => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const spaces = createSpaceRepository(application), runtime = createAppRuntimeRepository(application);
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }), deployment: "hosted" },
    spaces: { auth, appRuntime: runtime, repository: spaces }, appRuntime: { repository: runtime } });
  const account = async () => {
    const username = `member_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username, email: `${username}@example.invalid`, name: "Membership test", password: "test-password", analyticsEnabled: false }); users.push(result.user.id); return result;
  };
  const owner = await account(), member = await account(), other = await account();
  const { space } = await spaces.create(owner.user.id, { name: "Membership", template_id: "blank", integration_providers: [] }, "");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'member')", [space.id, member.user.id, other.user.id]);
  const request = (path: string, method: string, token = owner.token, body?: unknown) => app.request(path, { method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  return { app, spaces, runtime, owner, member, other, spaceId: space.id, request, notes: createJournalNotes(application, null), drawings: createJournalDrawings(application, null) };
}

async function activeWork(spaceId: string, userId: string, state = "awaiting_device") {
  const id = randomUUID(), deviceId = `device_${randomUUID()}`;
  await admin.query("INSERT INTO trusted_devices(id,user_id,name,public_key) VALUES($1,$2,'Test device',$3)", [deviceId, userId, randomUUID()]);
  await admin.query(`INSERT INTO space_runs(id,space_id,resource_kind,resource_id,agent_id,initiated_by_user_id,billing_user_id,requesting_member_id,trigger_kind,state,approval_state,device_wait_hook_token,device_wait_expires_at)
    VALUES($1,$2,'agent','test-agent','test-agent',$3,$3,$3,'manual',$4,'pending','old-hook',now()+interval '1 day')`, [id, spaceId, userId, state]);
  await admin.query(`INSERT INTO agent_run_jobs(run_id,space_id,agent_id,state,lease_owner,lease_expires_at)
    VALUES($1,$2,'test-agent','leased','old-worker',now()+interval '1 minute')`, [id, spaceId]);
  await admin.query(`INSERT INTO agent_run_tool_approvals(id,run_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token)
    VALUES($1,$1,$2,'call','tool','routine','hash','signed','hook')`, [id, userId]);
  await admin.query(`INSERT INTO agent_run_contexts(id,run_id,owner_user_id,space_id,device_id,kind,opaque_ref,expires_at)
    VALUES($1,$1,$2,$3,$4,'browser_tab','private-tab',now()+interval '1 day')`, [id, userId, spaceId, deviceId]);
  await admin.query(`INSERT INTO workflow_device_node_jobs(id,run_id,node_id,attempt,user_id,scope_id,operation,input,config,state,leased_device_id,lease_token_hash,lease_expires_at,context_id)
    VALUES($1,$1,'node',1,$2,'scope','operation','{}','{}','leased',$3,'old-token',now()+interval '1 minute',$1)`, [id, userId, deviceId]);
  await admin.query("INSERT INTO space_agents(id,space_id,creator_user_id,name,schedules_enabled) VALUES($1,$2,$3,'Scheduled',true)", [id, spaceId, userId]);
  await admin.query("INSERT INTO space_workflows(id,space_id,creator_user_id,name,stable_identifier,schedules_enabled) VALUES($1,$2,$3,'Scheduled',$1,true)", [id, spaceId, userId]);
  return id;
}

it("removes a member atomically, cancels their work and revokes both Journal rooms while preserving shared content", async () => {
  const f = await fixture(), memberId = f.member.user.id, actor = { userId: memberId };
  const ownRun = await activeWork(f.spaceId, memberId), otherRun = await activeWork(f.spaceId, f.other.user.id);
  const retrying = await activeWork(f.spaceId, memberId, "retrying"), completed = await activeWork(f.spaceId, memberId, "completed");
  const note = await f.notes.create(actor, f.spaceId, "Shared note"), drawing = await f.drawings.create(actor, f.spaceId, "Shared drawing");
  const conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Group',$3)", [conversation, f.spaceId, memberId]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2),($1,$3)", [conversation, memberId, f.other.user.id]);
  const listener = await admin.connect(), notifications: string[] = [];
  listener.on("notification", (message) => { if (message.payload?.includes(f.spaceId)) notifications.push(message.payload); });
  await listener.query("LISTEN misty_space_control");
  try {
    const response = await f.request(`/api/spaces/${f.spaceId}/members/${memberId}`, "DELETE"); expect(response.status, await response.text()).toBe(204);
    await expect.poll(() => notifications.length).toBe(1);
    expect(JSON.parse(notifications[0]!)).toEqual({ type: "member.removed", space_id: f.spaceId, user_ids: [memberId] });
  } finally { await listener.query("UNLISTEN misty_space_control"); listener.removeAllListeners("notification"); listener.release(); }
  for (const id of [ownRun, retrying]) {
    expect((await admin.query("SELECT state,runtime_phase,error_code,approval_state,device_wait_hook_token,device_wait_expires_at FROM space_runs WHERE id=$1", [id])).rows[0])
      .toEqual({ state: "canceled", runtime_phase: "canceled", error_code: "space_membership_revoked", approval_state: "denied", device_wait_hook_token: "", device_wait_expires_at: null });
    expect((await admin.query("SELECT state,lease_owner,lease_expires_at FROM agent_run_jobs WHERE run_id=$1", [id])).rows[0]).toEqual({ state: "canceled", lease_owner: null, lease_expires_at: null });
    expect((await admin.query("SELECT state FROM agent_run_tool_approvals WHERE run_id=$1", [id])).rows[0].state).toBe("denied");
    expect((await admin.query("SELECT state FROM agent_run_contexts WHERE run_id=$1", [id])).rows[0].state).toBe("detached");
    expect((await admin.query("SELECT state,lease_token_hash FROM workflow_device_node_jobs WHERE run_id=$1", [id])).rows[0]).toEqual({ state: "canceled", lease_token_hash: null });
  }
  expect((await admin.query("SELECT state FROM space_runs WHERE id=$1", [otherRun])).rows[0].state).toBe("awaiting_device");
  expect((await admin.query("SELECT state FROM space_runs WHERE id=$1", [completed])).rows[0].state).toBe("completed");
  for (const table of ["space_agents", "space_workflows"]) {
    expect((await admin.query(`SELECT schedules_enabled FROM ${table} WHERE id=$1`, [ownRun])).rows[0].schedules_enabled).toBe(false);
    expect((await admin.query(`SELECT schedules_enabled FROM ${table} WHERE id=$1`, [otherRun])).rows[0].schedules_enabled).toBe(true);
  }
  expect((await admin.query("SELECT user_id FROM space_conversation_members WHERE conversation_id=$1", [conversation])).rows.map((row) => row.user_id)).toEqual([f.other.user.id]);
  for (const [kind, id] of [["note", note.id], ["drawing", drawing.id]]) {
    expect((await admin.query(`SELECT lifecycle_state,creator_user_id,acl_version FROM space_${kind}s WHERE id=$1`, [id])).rows[0]).toEqual({ lifecycle_state: "active", creator_user_id: memberId, acl_version: "2" });
    expect((await admin.query(`SELECT command,payload FROM space_${kind}_control_outbox WHERE ${kind}_id=$1`, [id])).rows).toEqual([{ command: "acl", payload: { acl_version: 2 } }]);
  }
  await expect(f.notes.get(actor, f.spaceId, note.id)).rejects.toThrow("not_found");
  await expect(f.drawings.ticket(actor, f.spaceId, drawing.id)).rejects.toThrow("not_found");
  expect(await f.notes.get({ userId: f.other.user.id }, f.spaceId, note.id)).toMatchObject({ title: "Shared note", role: "editor" });
  expect((await f.request(`/spaces/${f.spaceId}/members/${memberId}`, "DELETE")).status).toBe(404);
});

it("keeps owner controls account-only and serializes leave/removal so one membership event commits", async () => {
  const f = await fixture(), path = `/spaces/${f.spaceId}/members/${f.member.user.id}`;
  expect((await f.request(`/spaces/${f.spaceId}/leave`, "POST")).status).toBe(400);
  expect((await f.request(`/spaces/${f.spaceId}/members/${f.owner.user.id}`, "DELETE")).status).toBe(400);
  expect((await f.request(path, "DELETE", f.other.token)).status).toBe(403);
  const installations = createInstallationRepository(application), catalog = createOfficialCatalog(), token = randomUUID();
  await installations.install(f.member.user.id, { ...catalog.find("terminal")!, scopes: ["spaces.read"] });
  await installations.session(f.member.user.id, "terminal", hashToken(token), f.spaceId);
  expect((await f.request(`/spaces/${f.spaceId}/leave`, "POST", token)).status).toBe(401);
  const currentSession = await f.runtime.findSession(hashToken(token));
  await expect(f.spaces.leave({ userId: f.member.user.id, appSession: currentSession! }, f.spaceId)).rejects.toThrow("forbidden");
  const results = await Promise.all([f.request(`/v1/spaces/${f.spaceId}/leave`, "POST", f.member.token), f.request(path, "DELETE")]);
  expect(results.filter((response) => response.status === 204)).toHaveLength(1);
  expect([403, 404]).toContain(results.find((response) => response.status !== 204)!.status);
  expect((await admin.query("SELECT count(*) FROM space_events WHERE space_id=$1 AND event_type IN ('member.left','member.removed')", [f.spaceId])).rows[0].count).toBe("1");
  await expect(f.spaces.members({ userId: f.member.user.id, appSession: currentSession! }, f.spaceId)).rejects.toThrow("forbidden");
  // Permission overrides remain the owner's policy for this identity on rejoin,
  // but old conversation membership and canceled work are not restored.
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [f.spaceId, f.member.user.id]);
  expect((await f.request(`/spaces/${f.spaceId}/leave`, "POST", f.member.token)).status).toBe(204);
});

it("rolls back membership, work, ACL changes and notifications when the durable event fails", async () => {
  const f = await fixture(), runId = await activeWork(f.spaceId, f.member.user.id);
  const note = await f.notes.create({ userId: f.member.user.id }, f.spaceId, "Rollback");
  await admin.query("REVOKE INSERT ON space_events FROM misty_hono_app_test");
  try { expect((await f.request(`/spaces/${f.spaceId}/leave`, "POST", f.member.token)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM space_members WHERE space_id=$1 AND user_id=$2", [f.spaceId, f.member.user.id])).rows[0].count).toBe("1");
  expect((await admin.query("SELECT state FROM space_runs WHERE id=$1", [runId])).rows[0].state).toBe("awaiting_device");
  expect((await admin.query("SELECT acl_version FROM space_notes WHERE id=$1", [note.id])).rows[0].acl_version).toBe("1");
  expect((await admin.query("SELECT count(*) FROM space_note_control_outbox WHERE note_id=$1", [note.id])).rows[0].count).toBe("0");
  expect((await f.request(`/spaces/${f.spaceId}/leave`, "POST", f.member.token)).status).toBe(204);
});

it("waits for authorized document writes before removing membership and revokes the newly committed document", async () => {
  const f = await fixture(), noteId = randomUUID();
  let release!: () => void, started!: (pid: number) => void;
  const gate = new Promise<void>((resolve) => { release = resolve; }), ready = new Promise<number>((resolve) => { started = resolve; });
  const writer = withTransaction(application, async (tx) => {
    await requireJournalMember(tx, { userId: f.member.user.id }, f.spaceId, "notes.write");
    await tx.query("INSERT INTO space_notes(id,space_id,creator_user_id,title_projection) VALUES($1,$2,$3,'Just committed')", [noteId, f.spaceId, f.member.user.id]);
    started((await tx.query("SELECT pg_backend_pid() AS pid")).rows[0].pid); await gate;
  }, { mode: "service" });
  const pid = await ready, removal = f.request(`/spaces/${f.spaceId}/leave`, "POST", f.member.token);
  try { await expect.poll(async () => (await admin.query("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))) AS waiting", [pid])).rows[0].waiting).toBe(true); }
  finally { release(); }
  await writer; expect((await removal).status).toBe(204);
  expect((await admin.query("SELECT acl_version FROM space_notes WHERE id=$1", [noteId])).rows[0].acl_version).toBe("2");
  await expect(f.notes.create({ userId: f.member.user.id }, f.spaceId, "Too late")).rejects.toThrow("space_forbidden");
});

async function transferable(f: Awaited<ReturnType<typeof fixture>>) {
  const { space } = await f.spaces.create(f.owner.user.id, { name: "Transferable", template_id: "blank", integration_providers: [] }, "");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'member')", [space.id, f.member.user.id, f.other.user.id]);
  return space.id;
}

it("transfers ownership and security domain atomically, preserving consumption across a lower plan and oversized storage", async () => {
  const f = await fixture(), id = await transferable(f), usage = createUsageRepository({ pool: application });
  expect(await (await f.request(`/spaces/${f.spaceId}/transfer`, "POST", f.owner.token, { user_id: f.member.user.id })).json()).toEqual({ code: "default_space_protected" });
  await admin.query("UPDATE licenses SET tier='pro' WHERE user_id=$1", [f.owner.user.id]);
  await usage.wallets({ userId: f.owner.user.id, spaceId: id });
  await admin.query("UPDATE space_hosted_ai_wallets SET weekly_consumed_microusd=250000,weekly_remaining_microusd=650000 WHERE space_id=$1", [id]);
  await admin.query("UPDATE space_storage_usage SET used_bytes=3000000000 WHERE space_id=$1", [id]);
  const oldDomain = (await admin.query("SELECT owner_user_id,version FROM security_domains WHERE space_id=$1", [id])).rows[0];
  const response = await f.request(`/api/spaces/${id}/transfer`, "POST", f.owner.token, { user_id: f.member.user.id }); expect(response.status, await response.text()).toBe(204);
  expect(await f.spaces.get({ userId: f.member.user.id }, id)).toMatchObject({ owner_user_id: f.member.user.id, role: "owner", is_default: false });
  expect(await f.spaces.get({ userId: f.owner.user.id }, id)).toMatchObject({ role: "member", permissions: { "space.transfer": false } });
  expect((await admin.query("SELECT owner_user_id,version FROM security_domains WHERE space_id=$1", [id])).rows[0]).toEqual({ owner_user_id: f.member.user.id, version: String(BigInt(oldDomain.version) + 1n) });
  expect((await admin.query("SELECT weekly_allowance_microusd,weekly_remaining_microusd,weekly_consumed_microusd FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0])
    .toEqual({ weekly_allowance_microusd: "150000", weekly_remaining_microusd: "0", weekly_consumed_microusd: "250000" });
  expect((await admin.query("SELECT used_bytes FROM space_storage_usage WHERE space_id=$1", [id])).rows[0].used_bytes).toBe("3000000000");
  expect((await admin.query("SELECT weekly_remaining_microusd FROM hosted_ai_wallets WHERE user_id=$1", [f.owner.user.id])).rows[0].weekly_remaining_microusd).toBe("900000");
  expect((await f.request(`/v1/spaces/${id}/transfer`, "POST", f.member.token, { user_id: f.owner.user.id })).status).toBe(204);
  expect((await admin.query("SELECT weekly_remaining_microusd FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0].weekly_remaining_microusd).toBe("650000");
});

it("blocks transfer with live reservations and reclaims expired member AI leases before applying the new allowance", async () => {
  const f = await fixture(), id = await transferable(f), usage = createUsageRepository({ pool: application });
  const { reservation } = await usage.reserve({ userId: f.other.user.id, spaceId: id, meter: "assistant_ai", idempotencyKey: randomUUID(), amount: 20000n });
  const transfer = () => f.request(`/spaces/${id}/transfer`, "POST", f.owner.token, { user_id: f.member.user.id });
  expect(await (await transfer()).json()).toEqual({ code: "version_conflict" });
  await admin.query("UPDATE hosted_ai_reservations SET lease_expires_at=now()-interval '1 second' WHERE id=$1", [reservation.id]);
  const rendition = randomUUID();
  await admin.query("INSERT INTO space_rendition_reservations(id,space_id,user_id,source_kind,source_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,'export',$1,1,'active',now()+interval '1 day')", [rendition, id, f.other.user.id]);
  expect(await (await transfer()).json()).toEqual({ code: "version_conflict" });
  // A rejected transfer rolls the attempted lease reclamation back as well.
  expect((await admin.query("SELECT status FROM hosted_ai_reservations WHERE id=$1", [reservation.id])).rows[0].status).toBe("reserved");
  await admin.query("UPDATE space_rendition_reservations SET state='released' WHERE id=$1", [rendition]);
  const upload = randomUUID();
  await admin.query(`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,
    requested_byte_size,client_sha256,state,upload_token_hash,expires_at)
    SELECT $1,id,security_domain_id,$3,$1,'pending.txt','library',1,$4,'initiated','test-token',now()+interval '1 hour' FROM spaces WHERE id=$2`,
    [upload, id, f.other.user.id, "a".repeat(64)]);
  await admin.query("INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,1,'active',now()+interval '1 hour')", [upload, id, f.other.user.id]);
  expect(await (await transfer()).json()).toEqual({ code: "version_conflict" });
  await admin.query("UPDATE space_upload_reservations SET state='released' WHERE upload_id=$1", [upload]);
  expect((await transfer()).status).toBe(204);
  expect((await admin.query("SELECT status FROM hosted_ai_reservations WHERE id=$1", [reservation.id])).rows[0].status).toBe("released");
  expect((await admin.query("SELECT reserved_microusd FROM hosted_ai_wallets WHERE user_id=$1", [f.other.user.id])).rows[0].reserved_microusd).toBe("0");
  expect((await admin.query("SELECT reserved_microusd FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0].reserved_microusd).toBe("0");
});

it("serializes competing transfers and creation against the recipient's final owned-Space slot", async () => {
  const f = await fixture(), first = await transferable(f), second = await transferable(f);
  const create = () => f.spaces.create(f.member.user.id, { name: "Recipient Space", template_id: "blank", integration_providers: [] }, "");
  await create(); const pending = await create();
  await admin.query("UPDATE spaces SET lifecycle_state='pending_deletion' WHERE id=$1", [pending.space.id]);
  const transfers = await Promise.all([first, second].map((id) => f.request(`/spaces/${id}/transfer`, "POST", f.owner.token, { user_id: f.member.user.id })));
  expect(transfers.map((response) => response.status).sort()).toEqual([204, 409]);
  expect(await transfers.find((response) => response.status === 409)!.json()).toEqual({ code: "space_ownership_limit_reached" });
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1 AND lifecycle_state<>'deleted'", [f.member.user.id])).rows[0].count).toBe("3");
  const recipient = f.other.user.id;
  for (let index = 0; index < 2; index++) await f.spaces.create(recipient, { name: "Other", template_id: "blank", integration_providers: [] }, "");
  const remaining = transfers[0]!.status === 409 ? first : second;
  const results = await Promise.allSettled([
    f.spaces.transfer({ userId: f.owner.user.id }, remaining, recipient),
    f.spaces.create(recipient, { name: "Last slot", template_id: "blank", integration_providers: [] }, ""),
  ]);
  expect(results.filter((result) => result.status === "fulfilled")).toHaveLength(1);
  for (const result of results) if (result.status === "rejected") expect(result.reason.message).toBe("space_ownership_limit_reached");
  expect((await admin.query("SELECT count(*) FROM spaces WHERE owner_user_id=$1 AND lifecycle_state<>'deleted'", [recipient])).rows[0].count).toBe("3");
});

it("rejects unauthorized transfer targets and rolls owner, roles and wallets back if the event fails", async () => {
  const f = await fixture(), id = await transferable(f), path = `/spaces/${id}/transfer`, body = { user_id: f.member.user.id };
  const installations = createInstallationRepository(application), token = randomUUID();
  await installations.install(f.owner.user.id, { ...createOfficialCatalog().find("terminal")!, scopes: ["spaces.read"] });
  await installations.session(f.owner.user.id, "terminal", hashToken(token), id);
  expect((await f.request(path, "POST", token, body)).status).toBe(401);
  expect((await f.request(path, "POST", f.other.token, body)).status).toBe(403);
  expect((await f.request(path, "POST", f.owner.token, { user_id: "missing" })).status).toBe(404);
  expect((await f.request(path, "POST", f.owner.token, { user_id: f.owner.user.id })).status).toBe(400);
  expect((await f.request(path, "POST", f.owner.token, { ...body, owner_user_id: f.other.user.id })).status).toBe(400);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.member.user.id]);
  expect((await f.request(path, "POST", f.owner.token, body)).status).toBe(404);
  await admin.query("UPDATE users SET lifecycle_state='active' WHERE id=$1", [f.member.user.id]);
  await admin.query("REVOKE INSERT ON space_events FROM misty_hono_app_test");
  try { expect((await f.request(path, "POST", f.owner.token, body)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_hono_app_test"); }
  expect((await admin.query("SELECT owner_user_id FROM spaces WHERE id=$1", [id])).rows[0].owner_user_id).toBe(f.owner.user.id);
  expect((await admin.query("SELECT owner_user_id FROM security_domains WHERE space_id=$1", [id])).rows[0].owner_user_id).toBe(f.owner.user.id);
  expect((await admin.query("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2", [id, f.member.user.id])).rows[0].role).toBe("member");
  expect((await admin.query("SELECT count(*) FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0].count).toBe("0");
  expect((await f.request(path, "POST", f.owner.token, body)).status).toBe(204);
});

it("requests Space deletion with a 30-day deadline, cancels all Space work and preserves recoverable documents", async () => {
  const f = await fixture(), id = await transferable(f), usage = createUsageRepository({ pool: application });
  const ownerRun = await activeWork(id, f.owner.user.id), memberRun = await activeWork(id, f.member.user.id), unaffectedRun = await activeWork(f.spaceId, f.owner.user.id);
  const actor = { userId: f.member.user.id }, note = await f.notes.create(actor, id, "Preserved"), drawing = await f.drawings.create(actor, id, "Preserved");
  const response = await f.request(`/v1/spaces/${id}`, "DELETE", f.owner.token, { confirmation: "Transferable" }); expect(response.status, await response.text()).toBe(204);
  expect((await admin.query("SELECT lifecycle_state,extract(epoch FROM permanent_delete_after-deletion_requested_at)::int AS seconds FROM spaces WHERE id=$1", [id])).rows[0])
    .toEqual({ lifecycle_state: "pending_deletion", seconds: 30 * 86400 });
  for (const run of [ownerRun, memberRun]) expect((await admin.query("SELECT state,error_code FROM space_runs WHERE id=$1", [run])).rows[0]).toEqual({ state: "canceled", error_code: "space_deleted" });
  expect((await admin.query("SELECT state FROM space_runs WHERE id=$1", [unaffectedRun])).rows[0].state).toBe("awaiting_device");
  for (const [kind, document] of [["note", note.id], ["drawing", drawing.id]]) {
    expect((await admin.query(`SELECT lifecycle_state,acl_version FROM space_${kind}s WHERE id=$1`, [document])).rows[0]).toEqual({ lifecycle_state: "active", acl_version: "2" });
    expect((await admin.query(`SELECT command FROM space_${kind}_control_outbox WHERE ${kind}_id=$1`, [document])).rows).toEqual([{ command: "acl" }]);
  }
  expect((await admin.query("SELECT count(*) FROM space_members WHERE space_id=$1", [id])).rows[0].count).toBe("3");
  expect((await f.spaces.list(f.owner.user.id)).spaces.map((space) => space.id)).toEqual([f.spaceId]);
  await expect(f.notes.get(actor, id, note.id)).rejects.toThrow("not_found");
  await expect(usage.wallets({ userId: f.member.user.id, spaceId: id })).rejects.toThrow("Space is unavailable");
  expect((await f.request(`/spaces/${id}`, "DELETE", f.owner.token, { confirmation: "Transferable" })).status).toBe(403);
  expect((await admin.query("SELECT count(*) FROM space_events WHERE space_id=$1 AND event_type='space.deletion_requested'", [id])).rows[0].count).toBe("1");
});

it("protects default Spaces, requires exact owner confirmation and rejects downloaded-app deletion", async () => {
  const f = await fixture(), id = await transferable(f);
  expect(await (await f.request(`/spaces/${f.spaceId}`, "DELETE", f.owner.token, { confirmation: "Membership" })).json()).toEqual({ code: "default_space_protected" });
  expect((await f.request(`/spaces/${id}`, "DELETE", f.member.token, { confirmation: "Transferable" })).status).toBe(403);
  for (const body of [{ confirmation: " Transferable" }, { confirmation: "transferable" }, { confirmation: null }, { confirmation: "Transferable", bypass: true }])
    expect((await f.request(`/spaces/${id}`, "DELETE", f.owner.token, body)).status).toBe(400);
  const installations = createInstallationRepository(application), token = randomUUID();
  await installations.install(f.owner.user.id, { ...createOfficialCatalog().find("terminal")!, scopes: ["spaces.read"] });
  await installations.session(f.owner.user.id, "terminal", hashToken(token), id);
  expect((await f.request(`/spaces/${id}`, "DELETE", token, { confirmation: "Transferable" })).status).toBe(401);
  expect((await f.request(`/api/spaces/${id}`, "DELETE", f.owner.token, { confirmation: "Transferable" })).status).toBe(204);
});

it("rolls back Space deletion, cancellation and collaboration revocation if the lifecycle update fails", async () => {
  const f = await fixture(), id = await transferable(f), run = await activeWork(id, f.member.user.id), note = await f.notes.create({ userId: f.member.user.id }, id, "Rollback");
  // A column privilege preserves the initial SELECT FOR UPDATE authorization;
  // the later lifecycle write must be the failure that rolls back prior effects.
  await admin.query("REVOKE UPDATE ON spaces FROM misty_hono_app_test; GRANT UPDATE(name) ON spaces TO misty_hono_app_test");
  try { expect((await f.request(`/spaces/${id}`, "DELETE", f.owner.token, { confirmation: "Transferable" })).status).toBe(500); }
  finally { await admin.query("REVOKE UPDATE(name) ON spaces FROM misty_hono_app_test; GRANT UPDATE ON spaces TO misty_hono_app_test"); }
  expect((await admin.query("SELECT lifecycle_state,permanent_delete_after FROM spaces WHERE id=$1", [id])).rows[0]).toEqual({ lifecycle_state: "active", permanent_delete_after: null });
  expect((await admin.query("SELECT state FROM space_runs WHERE id=$1", [run])).rows[0].state).toBe("awaiting_device");
  expect((await admin.query("SELECT acl_version FROM space_notes WHERE id=$1", [note.id])).rows[0].acl_version).toBe("1");
  expect((await admin.query("SELECT count(*) FROM space_note_control_outbox WHERE note_id=$1", [note.id])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT count(*) FROM space_events WHERE space_id=$1 AND event_type='space.deletion_requested'", [id])).rows[0].count).toBe("0");
});

it("delivers deletion notifications to a large Space in bounded payloads without dropping members", async () => {
  const f = await fixture(), id = await transferable(f), extra = Array.from({ length: 300 }, () => `bulk_${randomUUID()}`);
  users.push(...extra);
  await withTransaction(admin, async (tx) => {
    await tx.query(`INSERT INTO users(id,email,password_hash,username,license_id)
      SELECT value,value||'@example.invalid','test-only','bulk_'||left(md5(value),20),'license_'||value FROM unnest($1::text[]) value`, [extra]);
    await tx.query("INSERT INTO licenses(id,user_id,tier) SELECT 'license_'||value,value,'basic' FROM unnest($1::text[]) value", [extra]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) SELECT $1,value,'member' FROM unnest($2::text[]) value", [id, extra]);
  });
  const listener = await admin.connect(), notifications: string[] = [];
  listener.on("notification", (message) => { if (message.payload?.includes(id)) notifications.push(message.payload); });
  await listener.query("LISTEN misty_space_control");
  try {
    expect((await f.request(`/spaces/${id}`, "DELETE", f.owner.token, { confirmation: "Transferable" })).status).toBe(204);
    await expect.poll(() => notifications.flatMap((payload) => JSON.parse(payload).user_ids).length).toBe(303);
    expect(notifications.length).toBeGreaterThan(1);
    for (const payload of notifications) expect(Buffer.byteLength(payload)).toBeLessThanOrEqual(7500);
    expect(notifications.flatMap((payload) => JSON.parse(payload).user_ids).sort()).toEqual([...extra, f.owner.user.id, f.member.user.id, f.other.user.id].sort());
  } finally { await listener.query("UNLISTEN misty_space_control"); listener.removeAllListeners("notification"); listener.release(); }
});
