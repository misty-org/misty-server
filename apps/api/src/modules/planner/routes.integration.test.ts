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
import { createSpaceRepository } from "../spaces/repository.js";
import { createTaskRepository } from "./tasks/repository.js";
import { createCalendarRepository } from "./calendar/repository.js";

import { createCalendarSourceJobs } from "./calendar/source-jobs.js";
import { createCalendarSourceService } from "./calendar/source-service.js";
import { createCalendarSourceRepository } from "./calendar/source-repository.js";
import { createLegacyTokenBroker } from "../connections/legacy-token-broker.js";
import { createConnectionCipher } from "../connections/credentials.js";
import type { TokenRefresher } from "../connections/oauth-token.js";

const admin = createTestDatabase(), users: string[] = [];
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,owner_storage_usage,
      space_setup_integrations,space_creation_requests,space_events,user_app_installations,app_runtime_sessions,app_install_events,app_data_deletion_jobs TO misty_hono_app_test;
    GRANT SELECT ON space_member_permission_overrides,space_invitations,space_conversations,space_conversation_members,space_task_activity TO misty_hono_app_test;
    GRANT UPDATE ON space_conversation_members TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON space_tasks,space_task_counters,space_task_activity,native_task_effects TO misty_hono_app_test;
    GRANT SELECT ON space_library_items,space_message_attachments,space_roadmap_goal_tasks,space_roadmaps TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON personal_agents,space_roadmap_goals TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON space_native_calendar_events TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON space_calendar_events,space_calendar_sources,space_integrations,space_provider_credentials,space_workflow_event_claims TO misty_hono_app_test;
    GRANT SELECT ON provider_shared_resources TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON space_agent_instance_workflows,space_agent_instances TO misty_hono_app_test;
    GRANT SELECT ON space_roadmap_milestones,space_roadmap_nodes,space_roadmap_node_definitions TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON space_runs,agent_run_jobs,agent_run_tool_approvals,agent_run_contexts TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_agent_instances WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map((row) => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture(provider: { fetcher?: typeof fetch; refresh?: TokenRefresher; watchAddress?: string } = {}) {
  const cipher = createConnectionCipher(Buffer.alloc(32, 7));
  const broker = createLegacyTokenBroker({ pool: application, cipher, refresh: provider.refresh ?? (async () => { throw new Error("Unexpected token refresh"); }) });
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const tasks = createTaskRepository(application), runtime = createAppRuntimeRepository(application), installations = createInstallationRepository(application);
  const sources = createCalendarSourceService({ repository: createCalendarSourceRepository(application), broker, ...provider });
  const calendarJobs = createCalendarSourceJobs(application, sources);
  const app = createApi({ calendarCallbacks: calendarJobs, logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }), deployment: "hosted" },
    planner: { auth, appRuntime: runtime, tasks, calendar: createCalendarRepository(application), calendarSources: sources }, appRuntime: { repository: runtime } });
  const account = async () => {
    const username = `planner_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username, email: `${username}@example.invalid`, name: "Planner test", password: "test-password", analyticsEnabled: false }); users.push(result.user.id); return result;
  };
  const owner = await account(), member = await account(), outsider = await account();
  const { space } = await createSpaceRepository(application).create(owner.user.id, { name: "Planner", template_id: "blank", integration_providers: [] }, "");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [space.id, member.user.id]);
  const request = (path: string, token = owner.token) => app.request(path, { headers: { Authorization: `Bearer ${token}` } });
  const mutate = (method: string, path: string, body?: unknown, token = owner.token) => app.request(path, {
    method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const rpc = (method: string, params: unknown, token: string) => app.request("/v1/app-runtime/rpc", { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: JSON.stringify({ protocol: 2, method, params }) });
  let number = 0;
  const task = async (title: string, status = "todo", options: { priority?: string; due?: string; archived?: boolean; conversation?: string; assignee?: string } = {}) => {
    const id = `task_${randomUUID()}`; number++;
    await admin.query(`INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,priority,rank,due_at,due_timezone,source_refs,created_by_user_id,archived_at,audience_kind,audience_conversation_id,assignee_user_id,audience_creator_user_id)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'UTC','[]',$10,CASE WHEN $11 THEN now() END,$12,$13,$14,CASE WHEN $12='conversation' THEN $10::text END)`,
      [id, space.id, number, `MST-${number}`, title, status, options.priority ?? "medium", number * 1024, options.due ?? null, owner.user.id,
        options.archived ?? false, options.conversation ? "conversation" : "space", options.conversation ?? null, options.assignee ?? null]); return id;
  };
  const appToken = async (scopes = ["tasks.read"]) => {
    await installations.install(member.user.id, { ...createOfficialCatalog().find("terminal")!, scopes });
    const token = randomUUID(); await installations.session(member.user.id, "terminal", hashToken(token), space.id); return token;
  };
  const integration = async (userId = owner.user.id, expired = false) => {
    const id = randomUUID(), secret = cipher.encryptLegacy("google", Buffer.from(JSON.stringify({ access_token: "calendar-test-token", refresh_token: "calendar-refresh-test", token_type: "Bearer", expires_in: 3600 })));
    await admin.query("INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id) VALUES($1,$2,'google','Calendar','encrypted',$3)", [id, space.id, userId]);
    await admin.query("INSERT INTO space_provider_credentials(id,integration_id,space_id,user_id,provider,ciphertext,nonce,key_version,account_id,expires_at) VALUES($1,$2,$3,$4,'google',$5,$6,1,$2,$7)", [randomUUID(), id, space.id, userId, secret.ciphertext, secret.nonce, new Date(Date.now() + (expired ? -3600000 : 3600000))]);
    return id;
  };
  return { app, tasks, runtime, installations, owner, member, outsider, integration, cipher, calendarJobs, spaceId: space.id, request, mutate, rpc, task, appToken };
}

it("serves legacy task pagination, filters and unfiltered visible status totals through account aliases", async () => {
  const f = await fixture();
  const a = await f.task("Alpha & beta", "todo", { priority: "high", due: "2026-09-08T10:00:00Z", assignee: f.member.user.id });
  await f.task("Second", "done", { due: "2026-09-09T10:00:00Z" }); const c = await f.task("Third"); await f.task("Archived", "todo", { archived: true });
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.request(`${prefix}/spaces/${f.spaceId}/tasks?limit=1&status=todo`); expect(response.status).toBe(200);
    const page = await response.json(); expect(() => parseMethodResult("tasks.list", page)).not.toThrow();
    expect(page.tasks.map((item: { id: string }) => item.id)).toEqual([a]); expect(page.next_cursor).toBe("MQ");
    expect(page.status_totals).toEqual({ todo: 2, in_progress: 0, done: 1, canceled: 0 });
    const next = await (await f.request(`${prefix}/spaces/${f.spaceId}/tasks?limit=1&status=todo&cursor=${page.next_cursor}`)).json();
    expect(next.tasks.map((item: { id: string }) => item.id)).toEqual([c]); expect(next).not.toHaveProperty("next_cursor");
  }
  const query = new URLSearchParams({ q: "Alpha & beta", priority: "high", assignee_user_id: f.member.user.id, due_from: "2026-09-08T10:00:00Z", due_to: "2026-09-09T00:00:00Z", sort: "due" });
  expect((await (await f.request(`/spaces/${f.spaceId}/tasks?${query}`)).json()).tasks.map((item: { id: string }) => item.id)).toEqual([a]);
  expect((await (await f.request(`/spaces/${f.spaceId}/tasks?include_archived=true`)).json()).tasks).toHaveLength(4);
  expect((await (await f.request(`/spaces/${f.spaceId}/tasks?status=unknown`)).json()).tasks).toEqual([]);
});

it("forwards typed SDK query parameters without dropping filters, false values or URL-escaped text", async () => {
  const f = await fixture(), token = await f.appToken();
  const id = await f.task("A&B / 雪", "todo", { priority: "low" }); await f.task("Other"); await f.task("A&B / 雪", "todo", { archived: true, priority: "low" });
  const response = await f.rpc("tasks.list", { query: { q: "A&B / 雪", priority: "low", limit: 1, include_archived: false } }, token);
  expect(response.status, await response.clone().text()).toBe(200);
  const result = await response.json(); expect(() => parseMethodResult("tasks.list", result)).not.toThrow(); expect(result.tasks.map((item: { id: string }) => item.id)).toEqual([id]);
  expect(result).not.toHaveProperty("next_cursor");
  const archived = await f.rpc("tasks.list", { query: { q: "A&B / 雪", include_archived: true, limit: "1" } }, token);
  expect((await archived.json()).next_cursor).toBe("MQ");
  expect((await f.rpc("tasks.create", { body: { title: "Read-only session" } }, token)).status).toBe(403);
});

it("hides conversation-scoped tasks and activity from other members and rechecks permission and app revocation", async () => {
  const f = await fixture(), conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  const hidden = await f.task("Private", "todo", { conversation }), visible = await f.task("Visible");
  await admin.query("INSERT INTO space_task_activity(id,space_id,task_id,actor_kind,actor_user_id,kind,message) VALUES($1,$2,$3,'person',$4,'assigned','Private activity')", [randomUUID(), f.spaceId, hidden, f.owner.user.id]);
  const token = await f.appToken(), session = await f.runtime.findSession(hashToken(token));
  const page = await (await f.rpc("tasks.list", {}, token)).json(); expect(page.tasks.map((item: { id: string }) => item.id)).toEqual([visible]); expect(page.status_totals.todo).toBe(1);
  expect((await f.rpc("tasks.activity.list", { path: { taskID: hidden } }, token)).status).toBe(404);
  expect((await f.request(`/spaces/${f.spaceId}/tasks/${hidden}/activity`)).status).toBe(200);
  expect((await f.request(`/spaces/${f.spaceId}/tasks`, f.outsider.token)).status).toBe(403);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'tasks.view','deny',$3)", [f.spaceId, f.member.user.id, f.owner.user.id]);
  expect((await f.rpc("tasks.list", {}, token)).status).toBe(403);
  await admin.query("DELETE FROM space_member_permission_overrides WHERE space_id=$1", [f.spaceId]);
  await f.installations.uninstall(f.member.user.id, "terminal"); expect((await f.rpc("tasks.list", {}, token)).status).toBe(401);
  await expect(f.tasks.list({ userId: f.member.user.id, appSession: session! }, f.spaceId, {})).rejects.toBeInstanceOf(AppSessionRevoked);
});

it("preserves archived task activity and orders equal-time records deterministically", async () => {
  const f = await fixture(), taskId = await f.task("Archived", "done", { archived: true }), token = await f.appToken();
  for (const suffix of ["b", "a"]) await admin.query(`INSERT INTO space_task_activity(id,space_id,task_id,actor_kind,actor_user_id,kind,message,metadata,created_at)
    VALUES($1,$2,$3,'person',$4,'assigned',$5,'{"source":"test"}','2026-09-05T12:00:00Z')`, [`${taskId}_${suffix}`, f.spaceId, taskId, f.owner.user.id, suffix]);
  const response = await f.rpc("tasks.activity.list", { path: { taskID: taskId } }, token); expect(response.status).toBe(200);
  const result = await response.json(); expect(() => parseMethodResult("tasks.activity.list", result)).not.toThrow();
  expect(result.activity.map((item: { message: string }) => item.message)).toEqual(["a", "b"]); expect(result.activity[0]).not.toHaveProperty("actor_agent_id");
  expect((await f.request(`/spaces/${f.spaceId}/tasks/missing/activity`)).status).toBe(404);
});

it("validates query bounds and retains precise timestamp filtering and Go offset cursors", async () => {
  const f = await fixture(), id = await f.task("Precise", "todo", { due: "2026-09-05T10:00:00.000005Z" });
  for (const query of ["cursor=MQ==", "cursor=LTE", "cursor=MTAwMDAwMQ", "priority=invalid", "sort=invalid", "due_from=not-a-date", "due_from=2026-09-05T10:00Z", "due_from=2026-09-06T00:00:00Z&due_to=2026-09-05T00:00:00Z"])
    expect((await f.request(`/spaces/${f.spaceId}/tasks?${query}`)).status).toBe(400);
  const page = await (await f.request(`/spaces/${f.spaceId}/tasks?due_from=2026-09-05T10:00:00.000001Z&due_to=2026-09-05T10:00:00.000009Z`)).json();
  expect(page.tasks.map((item: { id: string }) => item.id)).toEqual([id]);
  expect((await f.request(`/spaces/${f.spaceId}/tasks?cursor=MA&limit=-1`)).status).toBe(200);
  expect((await f.request("/spaces/missing/tasks")).status).toBe(403);
});

it("runs all four task writes through native SDK dispatch with last-write-wins and an idempotent tombstone", async () => {
  const f = await fixture(), token = await f.appToken(["tasks.write"]);
  const create = await f.rpc("tasks.create", { body: { title: "  First  ", notes: "  Notes  " } }, token);
  expect(create.status, await create.clone().text()).toBe(201);
  const first = await create.json(); expect(() => parseMethodResult("tasks.create", first)).not.toThrow();
  expect(first).toMatchObject({ title: "First", notes: "Notes", status: "todo", priority: "medium", due_timezone: "UTC", version: 1, task_key: "MST-1", rank: 1024 });
  const update = await f.rpc("tasks.update", { path: { taskID: first.id }, body: { ...first, title: "Updated", status: "done", version: 1 } }, token);
  expect(update.status, await update.clone().text()).toBe(200);
  const updated = await update.json(); expect(() => parseMethodResult("tasks.update", updated)).not.toThrow();
  expect(updated).toMatchObject({ title: "Updated", status: "done", version: 2 }); expect(updated.completed_at).toBeTruthy();
  const move = await f.rpc("tasks.move", { path: { taskID: first.id }, body: { version: 1, status: "todo" } }, token);
  expect(move.status, await move.clone().text()).toBe(200);
  const moved = await move.json(); expect(() => parseMethodResult("tasks.move", moved)).not.toThrow();
  expect(moved.task).toMatchObject({ status: "todo", version: 3 }); expect(moved.task).not.toHaveProperty("completed_at");
  const remove = () => f.rpc("tasks.delete", { path: { taskID: first.id }, query: { version: 1 } }, token);
  const archivedResponse = await remove(); expect(archivedResponse.status, await archivedResponse.clone().text()).toBe(200);
  const archived = await archivedResponse.json(); expect(() => parseMethodResult("tasks.delete", archived)).not.toThrow();
  expect(archived.version).toBe(4); expect(archived.archived_at).toBeTruthy();
  expect(await (await remove()).json()).toEqual(archived);
  expect((await f.rpc("tasks.update", { path: { taskID: first.id }, body: { ...first, title: "Resurrect" } }, token)).status).toBe(404);
  expect((await f.rpc("tasks.move", { path: { taskID: first.id }, body: { version: 1, status: "done" } }, token)).status).toBe(404);
  const effects = (await admin.query("SELECT event_kind,task_version,payload FROM native_task_effects WHERE task_id=$1 ORDER BY id", [first.id])).rows;
  expect(effects.map(row => [row.event_kind, row.task_version])).toEqual([["created", "1"], ["updated", "2"], ["moved", "3"], ["archived", "4"]]);
  expect(effects[0].payload.task.id).toBe(first.id);
});

it("preserves REST aliases and rebalances crowded ranks while omitting private tasks from move responses", async () => {
  const f = await fixture(), ids: string[] = [];
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.mutate("POST", `${prefix}/spaces/${f.spaceId}/tasks`, { title: prefix || "root" });
    expect(response.status, await response.clone().text()).toBe(201); ids.push((await response.json()).id);
  }
  await admin.query("UPDATE space_tasks SET rank=1 WHERE id=$1", [ids[0]]);
  const conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  await admin.query("UPDATE space_tasks SET audience_kind='conversation',audience_conversation_id=$2,audience_creator_user_id=$3 WHERE id=$1", [ids[1], conversation, f.owner.user.id]);
  const response = await f.mutate("POST", `/v1/spaces/${f.spaceId}/tasks/${ids[2]}/move`, { version: 1, status: "todo", before_task_id: ids[0] }, f.member.token);
  expect(response.status, await response.clone().text()).toBe(200);
  const moved = await response.json(); expect(moved.task.rank).toBe(512);
  expect(moved.reordered.map((row: { id: string }) => row.id)).toEqual([ids[2], ids[0]]);
  expect((await admin.query("SELECT rank FROM space_tasks WHERE id=$1", [ids[0]])).rows[0].rank).toBe("1024");
});

it("denies unauthorized mutations and invalid references without changing stored tasks", async () => {
  const f = await fixture(), id = await f.task("Original"), token = await f.appToken();
  const path = `/spaces/${f.spaceId}/tasks/${id}`, body = { title: "Changed", status: "todo", version: 1 };
  expect((await f.mutate("PATCH", path, body, token)).status).toBe(403);
  expect((await f.mutate("DELETE", `${path}?version=1`, undefined, f.outsider.token)).status).toBe(403);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'tasks.manage','deny',$3)", [f.spaceId, f.member.user.id, f.owner.user.id]);
  expect((await f.mutate("PATCH", path, body, f.member.token)).status).toBe(403);
  expect((await f.mutate("PATCH", path, { ...body, assignee_user_id: f.outsider.user.id })).status).toBe(400);
  expect((await f.mutate("PATCH", path, { ...body, source_refs: [{ kind: "task_attachment", resource_id: "missing" }] })).status).toBe(404);
  for (const invalid of [{ ...body, title: " " }, { ...body, due_timezone: "Unknown/Zone" }, { ...body, source_refs: [{ kind: "unsupported", resource_id: "x" }] }]) {
    expect((await f.mutate("PATCH", path, invalid)).status).toBe(400);
  }
  const conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  await admin.query("UPDATE space_tasks SET audience_kind='conversation',audience_conversation_id=$2,audience_creator_user_id=$3 WHERE id=$1", [id, conversation, f.owner.user.id]);
  await admin.query("DELETE FROM space_member_permission_overrides WHERE space_id=$1", [f.spaceId]);
  expect((await f.mutate("PATCH", path, body, f.member.token)).status).toBe(404);
  expect((await f.mutate("DELETE", `${path}?version=1`, undefined, f.member.token)).status).toBe(404);
  expect((await admin.query("SELECT title,version FROM space_tasks WHERE id=$1", [id])).rows[0]).toEqual({ title: "Original", version: "1" });
});

it("records Agent assignment and cancels old run work when reassigned, retaining dispatch context privately", async () => {
  const f = await fixture(), agent = randomUUID(), run = randomUUID();
  await admin.query("INSERT INTO personal_agents(id,owner_user_id,name,model_id) VALUES($1,$2,'Task agent','test-model')", [agent, f.owner.user.id]);
  const agentRun = { mode: "server", context_references: [{ device_id: "device", kind: "browser_tab", opaque_ref: "private-tab", capabilities: ["browser.inspect"] }] };
  const response = await f.mutate("POST", `/spaces/${f.spaceId}/tasks`, { title: "Assigned", assignee_agent_id: agent, agent_run: agentRun });
  expect(response.status, await response.clone().text()).toBe(201);
  const task = await response.json(); expect(task.status).toBe("in_progress");
  const activity = await (await f.request(`/spaces/${f.spaceId}/tasks/${task.id}/activity`)).json();
  expect(activity.activity[0]).toMatchObject({ kind: "assigned", message: "Assigned to Agent", metadata: { agent_id: agent, task_version: 1 } });
  expect((await admin.query("SELECT payload FROM native_task_effects WHERE task_id=$1", [task.id])).rows[0].payload.agent_run).toEqual(agentRun);
  expect((await admin.query("SELECT payload FROM space_events WHERE entity_id=$1", [task.id])).rows[0].payload.task).not.toHaveProperty("agent_run");
  await admin.query(`INSERT INTO space_runs(id,space_id,resource_kind,resource_id,agent_id,initiated_by_user_id,billing_user_id,requesting_member_id,trigger_kind,state,source_task_id)
    VALUES($1,$2,'agent',$3,$3,$4,$4,$4,'task_assignment','running',$5)`, [run, f.spaceId, agent, f.owner.user.id, task.id]);
  await admin.query("INSERT INTO agent_run_jobs(run_id,space_id,agent_id,state,lease_owner,lease_expires_at) VALUES($1,$2,$3,'leased','worker',now()+interval '1 minute')", [run, f.spaceId, agent]);
  await admin.query(`INSERT INTO agent_run_tool_approvals(id,run_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token)
    VALUES($1,$1,$2,'call','tool','routine','hash','signed','hook')`, [run, f.owner.user.id]);
  const updated = await f.mutate("PATCH", `/spaces/${f.spaceId}/tasks/${task.id}`, { ...task, assignee_agent_id: "", assignee_user_id: f.member.user.id });
  expect(updated.status, await updated.clone().text()).toBe(200);
  expect((await admin.query("SELECT state,error_code FROM space_runs WHERE id=$1", [run])).rows[0]).toEqual({ state: "canceled", error_code: "task_unassigned" });
  expect((await admin.query("SELECT state,lease_owner FROM agent_run_jobs WHERE run_id=$1", [run])).rows[0]).toEqual({ state: "canceled", lease_owner: null });
  expect((await admin.query("SELECT state FROM agent_run_tool_approvals WHERE run_id=$1", [run])).rows[0].state).toBe("denied");
});

it("allocates concurrent task numbers and rolls back the task if its durable effect cannot be recorded", async () => {
  const f = await fixture();
  const results = await Promise.all(Array.from({ length: 3 }, (_, i) => f.mutate("POST", `/spaces/${f.spaceId}/tasks`, { title: `Task ${i}` })));
  for (const result of results) expect(result.status, await result.clone().text()).toBe(201);
  const tasks = await Promise.all(results.map(result => result.json()));
  expect(tasks.map(task => task.task_number).sort()).toEqual([1, 2, 3]);
  expect(tasks.map(task => task.rank).sort((a, b) => a - b)).toEqual([1024, 2048, 3072]);
  await admin.query("REVOKE INSERT ON native_task_effects FROM misty_hono_app_test");
  try { expect((await f.mutate("POST", `/spaces/${f.spaceId}/tasks`, { title: "Rollback" })).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON native_task_effects TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM space_tasks WHERE space_id=$1", [f.spaceId])).rows[0].count).toBe("3");
  expect((await admin.query("SELECT last_number FROM space_task_counters WHERE space_id=$1", [f.spaceId])).rows[0].last_number).toBe("3");
});

const calendarWindow = { from: "2026-09-05T00:00:00Z", to: "2026-09-08T00:00:00Z" };
const calendarBody = { title: " Meeting ", description: " Details ", location: " Desk ", starts_at: "2026-09-06T10:00:00Z", ends_at: "2026-09-06T11:00:00Z", timezone: "UTC" };
it("runs Calendar CRUD and agenda through SDK dispatch with optimistic versions and typed native DTOs", async () => {
  const f = await fixture(), token = await f.appToken(["calendar.read", "calendar.write", "tasks.read"]);
  const created = await f.rpc("calendar.events.create", { body: calendarBody }, token);
  expect(created.status, await created.clone().text()).toBe(201);
  const event = await created.json(); expect(() => parseMethodResult("calendar.events.create", event)).not.toThrow();
  expect(event).toMatchObject({ title: "Meeting", description: "Details", location: "Desk", provider: "misty", source_id: "misty", origin: "native", version: 1, organizer: null });
  const listed = await f.rpc("calendar.events.list", { query: calendarWindow }, token);
  expect(listed.status, await listed.clone().text()).toBe(200); expect((await listed.json()).events[0].id).toBe(event.id);
  const agenda = await f.rpc("agenda.list", { query: calendarWindow }, token);
  expect(agenda.status, await agenda.clone().text()).toBe(200);
  const snapshot = await agenda.json(); expect(() => parseMethodResult("agenda.list", snapshot)).not.toThrow();
  expect(snapshot.entries[0]).toMatchObject({ id: `event:${event.id}`, title: "Meeting", kind: "event", version: 1 });
  const updateBody = { ...event, title: "Changed", version: 1 };
  const update = await f.rpc("calendar.events.update", { path: { eventID: event.id }, body: updateBody }, token);
  expect(update.status, await update.clone().text()).toBe(200); expect((await update.json()).version).toBe(2);
  expect((await f.rpc("calendar.events.update", { path: { eventID: event.id }, body: updateBody }, token)).status).toBe(409);
  expect((await f.rpc("calendar.events.delete", { path: { eventID: event.id }, query: { version: 1 } }, token)).status).toBe(409);
  const removed = await f.rpc("calendar.events.delete", { path: { eventID: event.id }, query: { version: 2 } }, token);
  expect(removed.status, await removed.clone().text()).toBe(204); expect(await removed.text()).toBe("");
  expect((await (await f.rpc("agenda.list", { query: calendarWindow }, token)).json()).entries).toEqual([]);
  expect((await f.rpc("calendar.events.delete", { path: { eventID: event.id }, query: { version: 2 } }, token)).status).toBe(409);
  expect((await admin.query("SELECT event_type FROM space_events WHERE entity_id=$1 ORDER BY id", [event.id])).rows.map(row => row.event_type)).toEqual(["calendar.event.created", "calendar.event.updated", "calendar.event.archived"]);
});

it("enforces separate Calendar/agenda scopes and private event audience across REST aliases", async () => {
  const f = await fixture(), token = await f.appToken(["calendar.write"]), conversation = randomUUID();
  for (const prefix of ["", "/api", "/v1"]) {
    const created = await f.mutate("POST", `${prefix}/spaces/${f.spaceId}/calendar/events`, calendarBody, token);
    expect(created.status, await created.clone().text()).toBe(201);
  }
  expect((await f.rpc("calendar.events.list", { query: calendarWindow }, token)).status).toBe(403);
  expect((await f.rpc("agenda.list", { query: calendarWindow }, token)).status).toBe(403);
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  const created = await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, { ...calendarBody, title: "Private", audience_kind: "conversation", audience_conversation_id: conversation });
  expect(created.status, await created.clone().text()).toBe(201); const event = await created.json();
  const query = new URLSearchParams(calendarWindow);
  const read = await f.request(`/v1/spaces/${f.spaceId}/calendar/events?${query}`, f.member.token);
  expect((await read.json()).events.map((row: { title: string }) => row.title)).not.toContain("Private");
  expect((await f.mutate("PATCH", `/spaces/${f.spaceId}/calendar/events/${event.id}`, { ...event, title: "Leaked" }, f.member.token)).status).toBe(409);
  expect((await f.mutate("DELETE", `/spaces/${f.spaceId}/calendar/events/${event.id}?version=1`, undefined, f.member.token)).status).toBe(409);
  expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, calendarBody, f.outsider.token)).status).toBe(403);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'tasks.manage','deny',$3)", [f.spaceId, f.member.user.id, f.owner.user.id]);
  expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, calendarBody, token)).status).toBe(403);
});

it("combines task, provider, native and roadmap agenda dates and excludes canceled or archived entries", async () => {
  const f = await fixture(), roadmap = randomUUID(), milestone = randomUUID(), goal = randomUUID(), node = randomUUID(), integration = randomUUID(), source = randomUUID();
  const taskId = await f.task("Due task", "todo", { due: "2026-09-06T10:00:00Z" });
  await f.task("Canceled task", "canceled", { due: "2026-09-06T10:00:00Z" });
  await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, { ...calendarBody, title: "Native" });
  await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, { ...calendarBody, title: "Canceled event", status: "canceled" });
  await admin.query("INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id) VALUES($1,$2,'google','Calendar','test-only',$3)", [integration, f.spaceId, f.owner.user.id]);
  await admin.query(`INSERT INTO space_calendar_sources(id,space_id,integration_id,connected_by_user_id,provider,external_calendar_id,display_name)
    VALUES($1,$2,$3,$4,'google','primary','Calendar')`, [source, f.spaceId, integration, f.owner.user.id]);
  await admin.query(`INSERT INTO space_calendar_events(id,space_id,source_id,provider,external_event_id,fingerprint,title,starts_at,ends_at)
    VALUES($1,$2,$3,'google','remote-event','test','Imported','2026-09-06T09:00:00Z','2026-09-06T10:00:00Z')`, [randomUUID(), f.spaceId, source]);
  await admin.query("INSERT INTO space_roadmaps(id,space_id,name,created_by_user_id) VALUES($1,$2,'Dates',$3)", [roadmap, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_roadmap_milestones(id,space_id,roadmap_id,title,target_date,rank) VALUES($1,$2,$3,'Milestone','2026-09-06',1024)", [milestone, f.spaceId, roadmap]);
  await admin.query("INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,target_date,rank) VALUES($1,$2,$3,$4,'Goal','2026-09-06',1024)", [goal, f.spaceId, roadmap, milestone]);
  await admin.query("INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,node_kind,title,target_date) VALUES($1,$2,$3,'risk','Risk','2026-09-06')", [node, f.spaceId, roadmap]);
  const response = await f.request(`/spaces/${f.spaceId}/agenda?${new URLSearchParams(calendarWindow)}`);
  expect(response.status, await response.clone().text()).toBe(200);
  const snapshot = await response.json(); expect(() => parseMethodResult("agenda.list", snapshot)).not.toThrow();
  expect(snapshot.entries.map((entry: { title: string }) => entry.title)).toEqual(["Goal", "Milestone", "Risk", "Imported", "Native", "Due task"]);
  expect(snapshot.entries[5]).toMatchObject({ id: `task:${taskId}`, starts_at: "2026-09-06T10:00:00.000Z", ends_at: "2026-09-06T10:30:00.000Z" });
  const events = await (await f.request(`/spaces/${f.spaceId}/calendar/events?${new URLSearchParams(calendarWindow)}`)).json();
  expect(() => parseMethodResult("calendar.events.list", events)).not.toThrow();
  expect(events.events[0].title).toBe("Imported");
  expect(events.events.slice(1).map((entry: { title: string }) => entry.title).sort()).toEqual(["Canceled event", "Native"]);
  await admin.query("UPDATE space_roadmap_milestones SET archived_at=now() WHERE id=$1", [milestone]);
  const after = await (await f.request(`/spaces/${f.spaceId}/agenda?${new URLSearchParams(calendarWindow)}`)).json();
  expect(after.entries.map((entry: { title: string }) => entry.title)).not.toContain("Goal");
});

it("validates Calendar intervals and rolls back a mutation when its event cannot be recorded", async () => {
  const f = await fixture();
  for (const body of [{ ...calendarBody, title: " " }, { ...calendarBody, ends_at: "2026-09-06T09:00:00Z" }, { ...calendarBody, timezone: "Missing/Zone" }, { ...calendarBody, audience_conversation_id: "unexpected" }]) {
    expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, body)).status).toBe(400);
  }
  for (const query of [{ from: "bad", to: calendarWindow.to }, { from: calendarWindow.to, to: calendarWindow.from }, { from: "2026-01-01T00:00:00Z", to: "2028-01-01T00:00:00Z" }]) {
    expect((await f.request(`/spaces/${f.spaceId}/calendar/events?${new URLSearchParams(query)}`)).status).toBe(400);
  }
  const equal = await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, { ...calendarBody, ends_at: calendarBody.starts_at });
  expect(equal.status).toBe(201); // Existing Go accepts zero-duration events.
  await admin.query("REVOKE INSERT ON space_events FROM misty_hono_app_test");
  try { expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/events`, calendarBody)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_hono_app_test"); }
  expect((await admin.query("SELECT count(*) FROM space_native_calendar_events WHERE space_id=$1", [f.spaceId])).rows[0].count).toBe("1");
});

it("imports and refreshes Google calendar sources through all five native SDK routes", async () => {
  const requests: URL[] = []; let incremental = false, refreshes = 0;
  const f = await fixture({ refresh: async () => { refreshes++; return { access_token: "rotated-calendar-token", expires_in: 3600 }; }, fetcher: async (input, init) => {
    const url = new URL(String(input)); requests.push(url);
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer rotated-calendar-token");
    expect(init?.redirect).toBe("error");
    if (url.pathname.endsWith("/calendarList")) return Response.json({ items: [{ id: "shared/calendar@example.invalid", summary: "Team", timeZone: "America/Los_Angeles", accessRole: "reader" }, { id: "busy", accessRole: "freeBusyReader" }] });
    if (incremental) {
      expect(url.searchParams.get("syncToken")).toBe("cursor-1");
      return Response.json({ items: [{ id: "meeting", status: "cancelled" }], nextSyncToken: "cursor-2" });
    }
    return Response.json({ items: [{ id: "meeting", summary: "All day", status: "confirmed", start: { date: "2026-09-06" }, end: { date: "2026-09-07" }, htmlLink: "https://calendar.google.com/event", organizer: { email: "test@example.invalid" } }], nextSyncToken: "cursor-1" });
  } });
  const integrationId = await f.integration(f.member.user.id, true), token = await f.appToken(["calendar.read", "calendar.write", "connections.read", "tasks.write"]);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'integrations.manage','allow',$3)", [f.spaceId, f.member.user.id, f.owner.user.id]);
  const available = await f.rpc("calendar.google.calendars", { query: { integration_id: integrationId } }, token);
  expect(available.status, await available.clone().text()).toBe(200);
  const choices = await available.json(); expect(() => parseMethodResult("calendar.google.calendars", choices)).not.toThrow(); expect(choices.calendars).toHaveLength(1);
  const created = await f.rpc("calendar.sources.create", { body: { integration_id: integrationId, external_calendar_id: "shared/calendar@example.invalid" } }, token);
  expect(created.status, await created.clone().text()).toBe(201);
  const source = await created.json(); expect(() => parseMethodResult("calendar.sources.create", source)).not.toThrow(); expect(source).toMatchObject({ display_name: "Team", status: "pending", timezone: "America/Los_Angeles" });
  const list = await f.rpc("calendar.sources.list", {}, token); const snapshot = await list.json(); expect(() => parseMethodResult("calendar.sources.list", snapshot)).not.toThrow();
  expect(snapshot.sources[0].id).toBe("misty"); expect(snapshot.sources[1].status).toBe("active");
  expect(JSON.stringify(snapshot)).not.toMatch(/cursor-1|rotated-calendar-token|watch_token_hash|sync_token/);
  const imported = (await admin.query("SELECT starts_at,ends_at,all_day,meeting_url FROM space_calendar_events WHERE source_id=$1", [source.id])).rows[0];
  expect(imported.starts_at.toISOString()).toBe("2026-09-06T07:00:00.000Z"); expect(imported.ends_at.toISOString()).toBe("2026-09-07T07:00:00.000Z"); expect(imported.all_day).toBe(true);
  incremental = true;
  const sync = await f.rpc("calendar.sync", { body: { source_id: source.id } }, token);
  expect(sync.status, await sync.clone().text()).toBe(200); const synced = await sync.json(); expect(() => parseMethodResult("calendar.sync", synced)).not.toThrow(); expect(synced.tasks).toEqual([]);
  expect((await admin.query("SELECT removed_at,status FROM space_calendar_events WHERE source_id=$1", [source.id])).rows[0]).toMatchObject({ status: "canceled", removed_at: expect.any(Date) });
  expect(refreshes).toBe(1); expect(requests.some(url => url.pathname.includes("shared%2Fcalendar%40example.invalid"))).toBe(true);
  const secret = (await admin.query("SELECT ciphertext,nonce,key_version FROM space_provider_credentials WHERE integration_id=$1", [integrationId])).rows[0];
  expect(JSON.parse(f.cipher.decryptLegacy("google", secret.ciphertext, secret.nonce, secret.key_version).toString())).toMatchObject({ access_token: "rotated-calendar-token", refresh_token: "calendar-refresh-test" });
  expect((await f.rpc("calendar.sources.delete", { path: { sourceID: source.id } }, token)).status).toBe(204);
  expect((await admin.query("SELECT status,sync_token,watch_channel_id FROM space_calendar_sources WHERE id=$1", [source.id])).rows[0]).toEqual({ status: "disabled", sync_token: "", watch_channel_id: "" });
  const before = requests.length; await f.rpc("calendar.sync", { body: {} }, token); expect(requests).toHaveLength(before);
});

it("rebuilds an expired Google cursor and retains replay-safe workflow dispatch claims", async () => {
  let mode = "initial";
  const event = { id: "new", summary: "Imported", start: { dateTime: "2026-09-06T10:00:00Z" }, end: { dateTime: "2026-09-06T11:00:00Z" } };
  const f = await fixture({ fetcher: async input => {
    const url = new URL(String(input));
    if (url.pathname.endsWith("/calendarList")) return Response.json({ items: [{ id: "primary", summary: "Primary" }] });
    if (mode === "expired" && url.searchParams.has("syncToken")) return new Response(null, { status: 410 });
    return Response.json({ items: [mode === "initial" ? { ...event, id: "old" } : event], nextSyncToken: "cursor" });
  } });
  const integration = await f.integration();
  const create = await f.mutate("POST", `/api/spaces/${f.spaceId}/calendar/sources`, { integration_id: integration, external_calendar_id: "primary" }); expect(create.status).toBe(201); const source = await create.json();
  const agent = randomUUID(), version = randomUUID(), workflow = randomUUID(), workflowVersion = randomUUID(), instance = randomUUID();
  await admin.query("INSERT INTO space_agents(id,space_id,creator_user_id,name) VALUES($1,$2,$3,'Calendar agent')", [agent, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_agent_versions(id,agent_id,space_id,creator_user_id,version,name,access_policy,checksum_sha256) VALUES($1,$2,$3,$4,1,'Calendar agent','{}',$5)", [version, agent, f.spaceId, f.owner.user.id, "a".repeat(64)]);
  await admin.query("INSERT INTO space_workflows(id,space_id,creator_user_id,name,stable_identifier) VALUES($1,$2,$3,'Calendar workflow',$1)", [workflow, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_workflow_versions(id,workflow_id,space_id,stable_identifier,version,name,metadata,definition,checksum_sha256,created_by_user_id) VALUES($1,$2,$3,$2,'1.0.0','Calendar workflow','{}','{}',$4,$5)", [workflowVersion, workflow, f.spaceId, "a".repeat(64), f.owner.user.id]);
  await admin.query("INSERT INTO space_agent_instances(id,space_id,agent_id,user_id,agent_version_id) VALUES($1,$2,$3,$4,$5)", [instance, f.spaceId, agent, f.owner.user.id, version]);
  await admin.query(`INSERT INTO space_agent_instance_workflows(instance_id,workflow_version_id,enabled,trigger_config,consent) VALUES($1,$2,true,'{"kind":"connector_event","provider":"google","capabilityId":"handle-calendar"}','{"granted":true}')`, [instance, workflowVersion]);
  mode = "expired";
  const sync = () => f.mutate("POST", `/v1/spaces/${f.spaceId}/calendar/sync`, { source_id: source.id });
  expect((await sync()).status).toBe(200);
  expect((await admin.query("SELECT external_event_id FROM space_calendar_events WHERE source_id=$1 AND removed_at IS NULL", [source.id])).rows).toEqual([{ external_event_id: "new" }]);
  expect((await admin.query("SELECT * FROM space_workflow_event_claims WHERE instance_id=$1", [instance])).rows).toHaveLength(0);
  mode = "incremental"; expect((await sync()).status).toBe(200); expect((await sync()).status).toBe(200);
  const claims = (await admin.query("SELECT execution_owner,state,native_request FROM space_workflow_event_claims WHERE instance_id=$1", [instance])).rows;
  expect(claims).toHaveLength(1); expect(claims[0]).toMatchObject({ execution_owner: "hono", state: "claimed", native_request: { source_type: "connector", input: { event: { id: "new" }, trigger: { kind: "connector_event", provider: "google" } } } });
});

it("enforces source management and dual App scopes before touching provider credentials", async () => {
  let calls = 0;
  const f = await fixture({ fetcher: async () => { calls++; return new Response(null, { status: 403 }); } }), integration = await f.integration();
  const url = `/spaces/${f.spaceId}/calendar`, body = { integration_id: integration, external_calendar_id: "primary" };
  expect((await f.mutate("POST", `${url}/sources`, body, f.outsider.token)).status).toBe(403);
  expect((await f.mutate("POST", `${url}/sources`, body, f.member.token)).status).toBe(403);
  let token = await f.appToken(["calendar.read", "calendar.write"]);
  expect((await f.mutate("POST", `${url}/sync`, {}, token)).status).toBe(403);
  expect((await f.request(`${url}/google/calendars?integration_id=${integration}`, token)).status).toBe(403);
  expect(calls).toBe(0);
  const failure = await f.request(`${url}/google/calendars?integration_id=${integration}`);
  expect(failure.status).toBe(424); expect(await failure.json()).toEqual({ code: "permission_missing" });
  expect(calls).toBe(1);
});

it("does not resurrect a source disabled while a provider page was in flight", async () => {
  let f: Awaited<ReturnType<typeof fixture>>, disableDuringRead = false;
  f = await fixture({ fetcher: async input => {
    if (new URL(String(input)).pathname.endsWith("/calendarList")) return Response.json({ items: [{ id: "primary", summary: "Primary" }] });
    if (disableDuringRead) await admin.query("UPDATE space_calendar_sources SET status='disabled',disabled_at=now(),sync_token='',updated_at=clock_timestamp() WHERE space_id=$1", [f.spaceId]);
    return Response.json({ items: [{ id: "event", summary: disableDuringRead ? "Late" : "Initial", start: { dateTime: "2026-09-06T10:00:00Z" }, end: { dateTime: "2026-09-06T11:00:00Z" } }], nextSyncToken: "cursor" });
  } });
  const integration = await f.integration();
  const created = await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sources`, { integration_id: integration, external_calendar_id: "primary" }); expect(created.status).toBe(201);
  disableDuringRead = true;
  expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sync`, {})).status).toBe(200);
  expect((await admin.query("SELECT status,sync_token FROM space_calendar_sources WHERE space_id=$1", [f.spaceId])).rows[0]).toEqual({ status: "disabled", sync_token: "" });
  expect((await admin.query("SELECT title FROM space_calendar_events WHERE space_id=$1", [f.spaceId])).rows[0].title).toBe("Initial");
});

it("persists early Calendar callbacks, imports their generations and renews expiring watches", async () => {
  let f: Awaited<ReturnType<typeof fixture>>, imported = 0;
  const watches: Array<{ id: string; token: string; address: string; expiration: number }> = [];
  const callback = (channel: typeof watches[number], resource = "resource", prefix = "/api") => f.app.request(`${prefix}/provider-callbacks/google/calendar`, { method: "POST", headers: { "X-Goog-Channel-ID": channel.id, "X-Goog-Channel-Token": channel.token, "X-Goog-Resource-ID": resource } });
  f = await fixture({ watchAddress: "https://api.example.invalid/api/provider-callbacks/google/calendar", fetcher: async (input, init) => {
    const url = new URL(String(input));
    if (url.pathname.endsWith("/calendarList")) return Response.json({ items: [{ id: "primary", summary: "Primary" }] });
    if (url.pathname.endsWith("/watch")) {
      const channel = JSON.parse(String(init?.body)); watches.push(channel);
      expect(channel.type).toBe("web_hook"); expect(channel.address).toBe("https://api.example.invalid/api/provider-callbacks/google/calendar");
      expect((await callback(channel)).status).toBe(204); // Google can notify before registration returns.
      return Response.json({ id: channel.id, resourceId: "resource", expiration: String(channel.expiration) });
    }
    imported++; return Response.json({ items: [], nextSyncToken: `cursor-${imported}` });
  } });
  const integration = await f.integration();
  const created = await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sources`, { integration_id: integration, external_calendar_id: "primary" });
  expect(created.status, await created.clone().text()).toBe(201); const source = await created.json(); expect(watches).toHaveLength(1);
  const state = async () => (await admin.query("SELECT execution_owner,native_sync_requested,native_sync_completed,native_sync_lease_id,watch_resource_id FROM space_calendar_sources WHERE id=$1", [source.id])).rows[0];
  expect(await state()).toMatchObject({ execution_owner: "hono", native_sync_requested: "1", native_sync_completed: "0", native_sync_lease_id: null, watch_resource_id: "resource" });
  expect(await f.calendarJobs.runOnce()).toBe(true); expect(imported).toBe(2); expect(await state()).toMatchObject({ native_sync_completed: "1" });
  expect(await f.calendarJobs.runOnce()).toBe(false);
  expect((await callback({ ...watches[0]!, token: "invalid" })).status).toBe(404);
  expect((await callback(watches[0]!, "wrong-resource")).status).toBe(404);
  expect((await f.app.request("/provider-callbacks/google/calendar", { method: "POST" })).status).toBe(400);
  for (const prefix of ["", "/api", "/v1"]) expect((await callback(watches[0]!, "resource", prefix)).status).toBe(204);
  expect(await f.calendarJobs.runOnce()).toBe(true); expect(await state()).toMatchObject({ native_sync_completed: "4" });
  await admin.query("UPDATE space_calendar_sources SET watch_expires_at=now()+interval '1 hour',native_sync_available_at=now() WHERE id=$1", [source.id]);
  expect(await f.calendarJobs.runOnce()).toBe(true); expect(watches).toHaveLength(2); expect(watches[1]!.id).not.toBe(watches[0]!.id);
  expect((await callback(watches[0]!)).status).toBe(404);
  const listing = await (await f.request(`/spaces/${f.spaceId}/calendar/sources`)).json();
  expect(JSON.stringify(listing)).not.toMatch(/native_sync_|execution_owner|watch_token_hash|cursor-|gcal_/);
  expect((await f.mutate("DELETE", `/spaces/${f.spaceId}/calendar/sources/${source.id}`)).status).toBe(204);
  expect((await callback(watches[1]!)).status).toBe(404); expect(await f.calendarJobs.runOnce()).toBe(false);
});

it("leases each native Calendar refresh once and rolls back imports after lease loss", async () => {
  let release: (() => void) | undefined, started: (() => void) | undefined, pause = false;
  const waiting = new Promise<void>(resolve => { started = resolve; }), gate = new Promise<void>(resolve => { release = resolve; });
  const f = await fixture({ fetcher: async input => {
    if (new URL(String(input)).pathname.endsWith("/calendarList")) return Response.json({ items: [{ id: "primary", summary: "Primary" }] });
    if (pause) { started!(); await gate; }
    return Response.json({ items: [{ id: "event", summary: pause ? "Late" : "Initial", start: { dateTime: "2026-09-06T10:00:00Z" }, end: { dateTime: "2026-09-06T11:00:00Z" } }], nextSyncToken: "cursor" });
  } });
  const integration = await f.integration(), created = await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sources`, { integration_id: integration, external_calendar_id: "primary" }); expect(created.status).toBe(201);
  const source = await created.json(); await admin.query("UPDATE space_calendar_sources SET native_sync_available_at=now() WHERE id=$1", [source.id]);
  pause = true; const running = f.calendarJobs.runOnce();
  try {
    await waiting; expect(await f.calendarJobs.runOnce()).toBe(false);
    await admin.query("UPDATE space_calendar_sources SET native_sync_lease_until=now()-interval '1 second' WHERE id=$1", [source.id]);
  } finally { release!(); }
  expect(await running).toBe(true);
  expect((await admin.query("SELECT title FROM space_calendar_events WHERE source_id=$1", [source.id])).rows[0].title).toBe("Initial");
  pause = false; expect(await f.calendarJobs.runOnce()).toBe(true);
  expect((await admin.query("SELECT native_sync_lease_id FROM space_calendar_sources WHERE id=$1", [source.id])).rows[0].native_sync_lease_id).toBeNull();
});

it("leaves Go-owned Calendar sources outside native callbacks, scheduling and republication", async () => {
  let calls = 0;
  const f = await fixture({ fetcher: async input => {
    calls++; return Response.json(new URL(String(input)).pathname.endsWith("/calendarList") ? { items: [{ id: "primary", summary: "Primary" }] } : { items: [], nextSyncToken: "cursor" });
  } });
  const integration = await f.integration(), body = { integration_id: integration, external_calendar_id: "primary" };
  const created = await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sources`, body); expect(created.status).toBe(201); const source = await created.json();
  await admin.query("UPDATE space_calendar_sources SET execution_owner='go',native_sync_available_at=now(),last_reconciled_at=NULL WHERE id=$1", [source.id]);
  const before = calls; expect(await f.calendarJobs.runOnce()).toBe(false);
  expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sync`, { source_id: source.id })).status).toBe(200); expect(calls).toBe(before);
  expect((await f.mutate("POST", `/spaces/${f.spaceId}/calendar/sources`, body)).status).toBe(409);
  expect((await admin.query("SELECT execution_owner FROM space_calendar_sources WHERE id=$1", [source.id])).rows[0].execution_owner).toBe("go");
});
