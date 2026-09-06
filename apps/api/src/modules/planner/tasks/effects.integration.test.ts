import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { createTaskRepository } from "./repository.js";
import { createTaskEffects } from "./effects.js";
const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_task_effects_test') THEN CREATE ROLE misty_task_effects_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_task_effects_test;
    GRANT SELECT,UPDATE ON users,spaces,space_members,personal_agents,personal_agent_versions,trusted_devices,space_agent_instances,space_agent_instance_workflows TO misty_task_effects_test;
    GRANT SELECT ON space_member_permission_overrides,space_conversations,space_conversation_members,space_roadmap_goal_tasks,space_roadmaps TO misty_task_effects_test;
    GRANT SELECT,UPDATE ON space_roadmap_goals TO misty_task_effects_test;
    GRANT SELECT,INSERT,UPDATE ON space_tasks,space_task_counters,space_task_activity,space_events,native_task_effects,space_runs,
      agent_run_jobs,agent_run_contexts,space_workflow_event_claims TO misty_task_effects_test;
    GRANT USAGE,SELECT ON space_events_id_seq TO misty_task_effects_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_task_effects_test", max: 5 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM space_agent_instances WHERE space_id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM personal_agents WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = spaces.length = domains.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture(assigned = true, withContext = false) {
  const userId = `effect_${randomUUID().replaceAll("-", "").slice(0, 12)}`, spaceId = randomUUID(), domainId = randomUUID(), agentId = randomUUID(), versionId = randomUUID(), deviceId = `device_${randomUUID()}`;
  users.push(userId); spaces.push(spaceId); domains.push(domainId);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, `license_${userId}`]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [`license_${userId}`, userId]);
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domainId, userId, spaceId]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Task effects',$3)", [spaceId, userId, domainId]);
    await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [spaceId, userId]);
    await tx.query("INSERT INTO personal_agents(id,owner_user_id,name,model_id) VALUES($1,$2,'Worker','test-model')", [agentId, userId]);
    await tx.query(`INSERT INTO personal_agent_versions(id,agent_id,version,name,model_mode,model_id,checksum_sha256,created_by_user_id,default_run_mode)
      VALUES($1,$2,1,'Worker','automatic','test-model',$3,$4,'auto')`, [versionId, agentId, "a".repeat(64), userId]);
    await tx.query("INSERT INTO trusted_devices(id,user_id,name,public_key) VALUES($1,$2,'Test device',$3)", [deviceId, userId, randomUUID()]);
  });
  const context = { mode: "auto", context_references: [{ device_id: deviceId, kind: "browser_tab", opaque_ref: "tab-1", capabilities: ["browser.inspect", "browser.inspect"], metadata: { source: "test" } }] };
  const task = await createTaskRepository(application).create({ userId }, spaceId, { title: "Task", ...(assigned ? { assignee_agent_id: agentId } : {}), ...(withContext ? { agent_run: context } : {}) });
  const effectId = (await admin.query("SELECT id FROM native_task_effects WHERE task_id=$1", [task.id])).rows[0].id as string;
  const worker = createTaskEffects(application);
  return { userId, spaceId, agentId, versionId, deviceId, task, effectId, worker };
}
it("commits one assigned run/job and bounded device contexts under concurrent effect replay", async () => {
  const f = await fixture(true, true);
  const results = await Promise.all([f.worker.process(f.effectId), f.worker.process(f.effectId)]);
  expect(results.filter(Boolean)).toHaveLength(1);
  const runs = (await admin.query("SELECT id,state,execution_owner,effective_run_mode,agent_version_snapshot,action_envelope FROM space_runs WHERE source_task_id=$1", [f.task.id])).rows;
  expect(runs).toHaveLength(1); expect(runs[0]).toMatchObject({ state: "queued", execution_owner: "hono", effective_run_mode: "auto",
    agent_version_snapshot: { version_id: f.versionId, version: 1 }, action_envelope: { assignment_task_version: 1, approval_mode: "explicit_assignment" } });
  expect((await admin.query("SELECT state FROM agent_run_jobs WHERE run_id=$1", [runs[0].id])).rows[0].state).toBe("queued");
  expect((await admin.query("SELECT device_id,capabilities,state FROM agent_run_contexts WHERE run_id=$1", [runs[0].id])).rows).toEqual([{ device_id: f.deviceId, capabilities: ["browser.inspect"], state: "attached" }]);
  expect(await f.worker.process(f.effectId)).toBe(false);
  expect((await admin.query("SELECT state FROM native_task_effects WHERE id=$1", [f.effectId])).rows[0].state).toBe("completed");
});
it("skips stale assignment and cancels an effect after account access is revoked", async () => {
  const stale = await fixture();
  await admin.query("UPDATE space_tasks SET assignee_agent_id=NULL WHERE id=$1", [stale.task.id]);
  expect(await stale.worker.process(stale.effectId)).toBe(true);
  const disabled = await fixture();
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [disabled.userId]);
  expect(await disabled.worker.process(disabled.effectId)).toBe(true);
  expect((await admin.query("SELECT state,last_error_code FROM native_task_effects WHERE id=$1", [disabled.effectId])).rows[0]).toEqual({ state: "canceled", last_error_code: "task_actor_unavailable" });
  expect((await admin.query("SELECT id FROM space_runs WHERE source_task_id=ANY($1::text[])", [[stale.task.id, disabled.task.id]])).rows).toEqual([]);
});
it("rolls back downstream run creation and effect completion together when job insertion fails", async () => {
  const f = await fixture();
  await admin.query("REVOKE INSERT ON agent_run_jobs FROM misty_task_effects_test");
  try { await expect(f.worker.process(f.effectId)).rejects.toMatchObject({ code: "42501" }); }
  finally { await admin.query("GRANT INSERT ON agent_run_jobs TO misty_task_effects_test"); }
  expect((await admin.query("SELECT id FROM space_runs WHERE source_task_id=$1", [f.task.id])).rows).toEqual([]);
  expect((await admin.query("SELECT state FROM native_task_effects WHERE id=$1", [f.effectId])).rows[0].state).toBe("pending");
  expect(await f.worker.process(f.effectId)).toBe(true);
});
it("retains failed device admission for retry without creating a partially authorized run", async () => {
  const f = await fixture(true, true);
  await admin.query("UPDATE trusted_devices SET revoked_at=now() WHERE id=$1", [f.deviceId]);
  await expect(f.worker.runOnce()).rejects.toThrow("task_device_unavailable");
  expect((await admin.query("SELECT state,last_error_code,attempts FROM native_task_effects WHERE id=$1", [f.effectId])).rows[0]).toEqual({ state: "pending", last_error_code: "task_device_unavailable", attempts: 1 });
  expect((await admin.query("SELECT id FROM space_runs WHERE source_task_id=$1", [f.task.id])).rows).toEqual([]);
});
async function workflow(f: Awaited<ReturnType<typeof fixture>>, consent: boolean) {
  const agentId = randomUUID(), agentVersion = randomUUID(), workflowId = randomUUID(), workflowVersion = randomUUID(), instance = randomUUID();
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO space_agents(id,space_id,creator_user_id,name) VALUES($1,$2,$3,'Workflow agent')", [agentId, f.spaceId, f.userId]);
    await tx.query(`INSERT INTO space_agent_versions(id,agent_id,space_id,creator_user_id,version,name,access_policy,checksum_sha256)
      VALUES($1,$2,$3,$4,1,'Workflow agent','{}',$5)`, [agentVersion, agentId, f.spaceId, f.userId, "a".repeat(64)]);
    await tx.query("INSERT INTO space_workflows(id,space_id,creator_user_id,name,stable_identifier) VALUES($1,$2,$3,'Task workflow',$1)", [workflowId, f.spaceId, f.userId]);
    await tx.query(`INSERT INTO space_workflow_versions(id,workflow_id,space_id,stable_identifier,version,name,metadata,definition,checksum_sha256,created_by_user_id)
      VALUES($1,$2,$3,$2,'1.0.0','Task workflow','{}','{}',$4,$5)`, [workflowVersion, workflowId, f.spaceId, "a".repeat(64), f.userId]);
    await tx.query("INSERT INTO space_agent_instances(id,space_id,agent_id,user_id,agent_version_id) VALUES($1,$2,$3,$4,$5)", [instance, f.spaceId, agentId, f.userId, agentVersion]);
    await tx.query(`INSERT INTO space_agent_instance_workflows(instance_id,workflow_version_id,enabled,trigger_config,consent)
      VALUES($1,$2,true,'{"kind":"task_change","capabilityId":"handle-task"}',$3::jsonb)`, [instance, workflowVersion, JSON.stringify({ granted: consent })]);
  }); return { instance, workflowVersion, agentId };
}
it("fans out consented workflow claims with replay identity and durable native dispatch inputs", async () => {
  const f = await fixture(false), active = await workflow(f, true); await workflow(f, false);
  expect(await f.worker.process(f.effectId)).toBe(true);
  const claims = (await admin.query("SELECT * FROM space_workflow_event_claims WHERE instance_id=$1", [active.instance])).rows;
  expect(claims).toHaveLength(1); expect(claims[0]).toMatchObject({ event_id: `created:${f.task.id}:v1`, state: "claimed", execution_owner: "hono", run_id: null,
    native_request: { requesting_member_id: f.userId, agent_id: active.agentId, capability_id: "handle-task", input: { event: { eventKind: "created" } } } });
  await admin.query("UPDATE native_task_effects SET state='pending',completed_at=NULL WHERE id=$1", [f.effectId]);
  expect(await f.worker.process(f.effectId)).toBe(true);
  expect((await admin.query("SELECT count(*) FROM space_workflow_event_claims WHERE instance_id IN(SELECT id FROM space_agent_instances WHERE space_id=$1)", [f.spaceId])).rows[0].count).toBe("1");
});
