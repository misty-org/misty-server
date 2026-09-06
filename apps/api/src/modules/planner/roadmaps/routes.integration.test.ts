import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { parseMethodResult } from "@misty/contracts";
import { applyMigrations, readMigrations } from "../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { createApi } from "../../../app.js";
import { createAuthRepository } from "../../auth/repository.js";
import { createAuthService, hashToken } from "../../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../../auth/passwords.js";
import { createAppRuntimeRepository } from "../../app-runtime/repository.js";
import { createOfficialCatalog } from "../../official-apps/catalog.js";
import { createInstallationRepository } from "../../official-apps/repository.js";
import { createSpaceRepository } from "../../spaces/repository.js";
import { createTaskRepository } from "../tasks/repository.js";
import { createRoadmapRepository } from "./repository.js";
const admin = createTestDatabase(), users: string[] = [];
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_roadmap_test') THEN CREATE ROLE misty_roadmap_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_roadmap_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,owner_storage_usage,
      space_setup_integrations,space_creation_requests,space_events,user_app_installations,app_runtime_sessions,app_install_events,app_data_deletion_jobs TO misty_roadmap_test;
    GRANT SELECT ON space_member_permission_overrides,space_invitations,space_conversations,space_conversation_members,space_tasks,
      space_roadmap_goals,space_roadmap_nodes,space_roadmap_node_definitions,space_roadmap_edges,space_roadmap_goal_tasks TO misty_roadmap_test;
    GRANT SELECT,INSERT,UPDATE ON space_roadmaps,space_roadmap_milestones,space_roadmap_goals,space_roadmap_nodes,space_roadmap_node_definitions TO misty_roadmap_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON space_roadmap_goal_tasks,space_roadmap_edges TO misty_roadmap_test;
    GRANT UPDATE ON space_tasks TO misty_roadmap_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_roadmap_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_roadmap_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map(row => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});

it("executes edge CRUD and layout SDK methods with legacy goal endpoints and atomic graph versions", async () => {
  const f = await fixture(), token = await f.appToken(), snapshot = await (await f.rpc("roadmaps.create", { body: { name: "Canvas" } }, token)).json();
  const roadmapID = snapshot.roadmap.id, first = snapshot.milestones[0].id, second = randomUUID(), goals = [randomUUID(), randomUUID()], node = randomUUID();
  await admin.query("INSERT INTO space_roadmap_milestones(id,space_id,roadmap_id,title,rank) VALUES($1,$2,$3,'Second',2048)", [second, f.spaceId, roadmapID]);
  for (const [index, id] of goals.entries()) await admin.query("INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,rank) VALUES($1,$2,$3,$4,'Goal',$5)", [id, f.spaceId, roadmapID, first, (index + 1) * 1024]);
  await admin.query("INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,milestone_id,node_kind,title) VALUES($1,$2,$3,$4,'note','Note')", [node, f.spaceId, roadmapID, first]);
  const created = await f.rpc("roadmaps.edges.create", { path: { roadmapID }, body: { source_goal_id: goals[0], target_goal_id: goals[1], edge_type: "dependency", label: " Link ", expected_version: 1 } }, token);
  expect(created.status, await created.clone().text()).toBe(201); const initial = await created.json();
  expect(() => parseMethodResult("roadmaps.edges.create", initial)).not.toThrow();
  expect(initial).toMatchObject({ graph_version: 2, edge: { source: { kind: "goal", id: goals[0] }, target: { kind: "goal", id: goals[1] }, source_goal_id: goals[0], target_goal_id: goals[1], edge_type: "depends_on", label: "Link", version: 1 } });
  const updated = await f.rpc("roadmaps.edges.update", { path: { roadmapID, edgeID: initial.edge.id }, body: { source: { kind: "node", id: node }, target: { kind: "milestone", id: second }, edge_type: "documents", expected_version: 2 } }, token);
  expect(updated.status, await updated.clone().text()).toBe(200); const edit = await updated.json();
  expect(() => parseMethodResult("roadmaps.edges.update", edit)).not.toThrow(); expect(edit).toMatchObject({ graph_version: 3, edge: { version: 2, edge_type: "documents", label: "" } });
  expect(edit.edge).not.toHaveProperty("source_goal_id"); expect(edit.edge).not.toHaveProperty("target_goal_id");
  const layout = await f.rpc("roadmaps.layout.update", { path: { roadmapID }, body: { expected_version: 3,
    milestones: [{ id: second, position_x: 500, position_y: -12, width: 600, height: 800, title: "Ignored" }],
    goals: [{ id: goals[0], milestone_id: second, position_x: 100, position_y: 200 }], nodes: [{ id: node, position_x: -40, position_y: 7 }] } }, token);
  expect(layout.status, await layout.clone().text()).toBe(200); const positioned = await layout.json(); expect(() => parseMethodResult("roadmaps.layout.update", positioned)).not.toThrow(); expect(positioned.graph_version).toBe(4);
  const graph = await (await f.rpc("roadmaps.get", { path: { roadmapID } }, token)).json();
  expect(graph.milestones.find((item: { id: string }) => item.id === second)).toMatchObject({ title: "Second", position_x: 500, position_y: -12, width: 600, height: 800, version: 2 });
  expect(graph.goals.find((item: { id: string }) => item.id === goals[0])).toMatchObject({ milestone_id: second, position_x: 100, position_y: 200, version: 2 });
  expect(graph.nodes[0]).toMatchObject({ position_x: -40, position_y: 7, version: 2 }); expect(graph.nodes[0]).not.toHaveProperty("milestone_id");
  const removed = await f.rpc("roadmaps.edges.delete", { path: { roadmapID, edgeID: initial.edge.id }, query: { expected_version: 4 } }, token);
  expect(removed.status).toBe(200); const result = await removed.json(); expect(() => parseMethodResult("roadmaps.edges.delete", result)).not.toThrow(); expect(result.graph_version).toBe(5);
  expect((await admin.query("SELECT event_type FROM space_events WHERE entity_id=$1 ORDER BY id", [initial.edge.id])).rows.map(row => row.event_type)).toEqual(["roadmap.edge.updated", "roadmap.edge.updated", "roadmap.edge.removed"]);
});

it("rejects causal cycles, invalid endpoint types and failed layouts without committing partial edits", async () => {
  const f = await fixture(), root = `/spaces/${f.spaceId}/roadmaps`, snapshot = await (await f.request("POST", root, { name: "Graph" })).json();
  const roadmap = snapshot.roadmap.id, milestone = snapshot.milestones[0].id, path = `${root}/${roadmap}`, goals = [randomUUID(), randomUUID(), randomUUID()], node = randomUUID();
  for (const [index, id] of goals.entries()) await admin.query("INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,rank) VALUES($1,$2,$3,$4,'Goal',$5)", [id, f.spaceId, roadmap, milestone, (index + 1) * 1024]);
  await admin.query("INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,node_kind,title) VALUES($1,$2,$3,'risk','Risk')", [node, f.spaceId, roadmap]);
  const edge = (source: string, target: string, expected_version: number, edge_type = "depends_on") => ({ source: { kind: "goal", id: source }, target: { kind: "goal", id: target }, edge_type, expected_version });
  expect((await f.request("POST", `${path}/edges`, edge(goals[0]!, goals[1]!, 1))).status).toBe(201);
  expect((await f.request("POST", `${path}/edges`, edge(goals[1]!, goals[2]!, 2, "enables"))).status).toBe(201);
  expect((await f.request("POST", `${path}/edges`, edge(goals[2]!, goals[0]!, 3, "blocks"))).status).toBe(400);
  expect((await f.request("POST", `${path}/edges`, { ...edge(node, goals[0]!, 3), source: { kind: "node", id: node }, edge_type: "measures" })).status).toBe(400);
  expect((await f.request("POST", `${path}/edges`, { ...edge(node, goals[0]!, 3), source: { kind: "node", id: node }, edge_type: "blocks" })).status).toBe(201);
  const other = await (await f.request("POST", root, { name: "Other" })).json();
  const layout = { expected_version: 4, milestones: [{ id: milestone, position_x: 999, position_y: 888, width: 500, height: 400 }], goals: [{ id: goals[0], milestone_id: other.milestones[0].id, position_x: 10 }] };
  expect((await f.request("PATCH", `/api${path}/layout`, layout)).status).toBe(404);
  expect((await admin.query("SELECT position_x,version FROM space_roadmap_milestones WHERE id=$1", [milestone])).rows[0]).toEqual({ position_x: 80, version: "1" });
  expect((await admin.query("SELECT graph_version FROM space_roadmaps WHERE id=$1", [roadmap])).rows[0].graph_version).toBe("4");
  expect((await f.request("PATCH", `${path}/layout`, { ...layout, milestones: [{ ...layout.milestones[0], width: 1 }], goals: [] })).status).toBe(400);
  expect((await f.request("PATCH", `${path}/layout`, { expected_version: 3 })).status).toBe(409);
  const readOnly = await f.appToken(["roadmaps.read"]); expect((await f.request("PATCH", `/v1${path}/layout`, { expected_version: 4 }, readOnly)).status).toBe(403);
  await admin.query("REVOKE INSERT ON space_events FROM misty_roadmap_test");
  try { expect((await f.request("PATCH", `${path}/layout`, { ...layout, goals: [] })).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_roadmap_test"); }
  expect((await admin.query("SELECT position_x,version FROM space_roadmap_milestones WHERE id=$1", [milestone])).rows[0]).toEqual({ position_x: 80, version: "1" });
});

it("serves seven definition and node SDK methods, retaining editable nodes after definition archival", async () => {
  const f = await fixture(), token = await f.appToken();
  const schema = [{ id: "score", label: "Score", type: "number" }, { id: "approved", label: "Approved", type: "checkbox" }, { id: "choice", label: "Choice", type: "select", options: ["A", "B"] }];
  const body = { name: "  Custom  ", icon: "shapes", color: "blue", agenda_visible: true, field_schema: schema };
  const created = await f.rpc("roadmaps.nodeDefinitions.create", { body: { ...body, created_by_user_id: f.outsider.user.id } }, token);
  expect(created.status, await created.clone().text()).toBe(201); const definition = await created.json();
  expect(() => parseMethodResult("roadmaps.nodeDefinitions.create", definition)).not.toThrow();
  expect(definition).toMatchObject({ name: "Custom", version: 1, created_by_user_id: f.member.user.id });
  const listed = await (await f.rpc("roadmaps.nodeDefinitions.list", {}, token)).json();
  expect(() => parseMethodResult("roadmaps.nodeDefinitions.list", listed)).not.toThrow(); expect(listed.node_definitions).toEqual([definition]);
  const graph = await (await f.rpc("roadmaps.create", { body: { name: "Nodes" } }, token)).json(), roadmapID = graph.roadmap.id;
  const nodeBody = { title: " Risk ", node_kind: "custom", definition_id: definition.id, milestone_id: graph.milestones[0].id,
    position_x: 33, target_date: "2026-09-11T23:00:00-07:00", field_values: { score: 4.5, approved: false, choice: "A" } };
  const nodeCreated = await f.rpc("roadmaps.nodes.create", { path: { roadmapID }, body: { ...nodeBody, expected_version: 1 } }, token);
  expect(nodeCreated.status, await nodeCreated.clone().text()).toBe(201); const initial = await nodeCreated.json();
  expect(() => parseMethodResult("roadmaps.nodes.create", initial)).not.toThrow();
  expect(initial).toMatchObject({ graph_version: 2, node: { title: "Risk", position_x: 33, position_y: 0, target_date: "2026-09-11T00:00:00.000Z", field_values: nodeBody.field_values, version: 1 } });
  const edited = await f.rpc("roadmaps.nodeDefinitions.update", { path: { definitionID: definition.id }, body: { ...body, name: "Renamed", field_schema: [...schema, { id: "memo", label: "Memo", type: "long_text", archived: true }], expected_version: 1 } }, token);
  expect(edited.status, await edited.clone().text()).toBe(200); const definitionEdited = await edited.json();
  expect(() => parseMethodResult("roadmaps.nodeDefinitions.update", definitionEdited)).not.toThrow(); expect(definitionEdited.version).toBe(2);
  expect((await f.rpc("roadmaps.nodeDefinitions.delete", { path: { definitionID: definition.id }, query: { expected_version: 2 } }, token)).status).toBe(204);
  expect((await (await f.rpc("roadmaps.nodeDefinitions.list", {}, token)).json()).node_definitions).toEqual([]);
  expect((await f.rpc("roadmaps.nodes.create", { path: { roadmapID }, body: { ...nodeBody, expected_version: 2 } }, token)).status).toBe(404);
  const nodeUpdated = await f.rpc("roadmaps.nodes.update", { path: { roadmapID, nodeID: initial.node.id }, body: { ...nodeBody, title: "Edited", field_values: { ...nodeBody.field_values, memo: "Archived field still readable" }, expected_version: 2 } }, token);
  expect(nodeUpdated.status, await nodeUpdated.clone().text()).toBe(200); const updated = await nodeUpdated.json();
  expect(() => parseMethodResult("roadmaps.nodes.update", updated)).not.toThrow(); expect(updated).toMatchObject({ graph_version: 3, node: { title: "Edited", version: 2 } });
  const snapshot = await (await f.rpc("roadmaps.get", { path: { roadmapID } }, token)).json();
  expect(snapshot.node_definitions[0]).toMatchObject({ id: definition.id, version: 3 }); expect(snapshot.node_definitions[0].archived_at).toBeTruthy();
  const edge = randomUUID(); await admin.query("INSERT INTO space_roadmap_edges(id,space_id,roadmap_id,source_kind,source_id,target_kind,target_id,edge_type) VALUES($1,$2,$3,'milestone',$4,'node',$5,'related')", [edge, f.spaceId, roadmapID, graph.milestones[0].id, initial.node.id]);
  const removed = await f.rpc("roadmaps.nodes.delete", { path: { roadmapID, nodeID: initial.node.id }, query: { expected_version: 3 } }, token);
  expect(removed.status).toBe(200); const result = await removed.json(); expect(() => parseMethodResult("roadmaps.nodes.delete", result)).not.toThrow(); expect(result.graph_version).toBe(4);
  expect((await admin.query("SELECT id FROM space_roadmap_edges WHERE id=$1", [edge])).rowCount).toBe(0);
  expect((await admin.query("SELECT payload FROM space_events WHERE entity_id=$1 AND event_type='roadmap.node.created'", [initial.node.id])).rows[0].payload).toEqual({ roadmap_id: roadmapID, graph_version: 2, node_kind: "custom" });
});

it("validates definition evolution and typed node fields without consuming graph versions", async () => {
  const f = await fixture(), root = `/spaces/${f.spaceId}`, definitions = `${root}/roadmap-node-definitions`;
  const schema = [{ id: "score", label: "Score", type: "number" }, { id: "choice", label: "Choice", type: "select", options: ["A"] }];
  const body = { name: "Custom", icon: "shapes", color: "blue", field_schema: schema };
  for (const field_schema of [null, [{ id: "Bad", label: "Bad", type: "number" }], [schema[0], schema[0]], [{ ...schema[1], options: ["A", " A "] }]]) {
    expect((await f.request("POST", definitions, { ...body, field_schema })).status).toBe(400);
  }
  const definition = await (await f.request("POST", definitions, body)).json();
  for (const field_schema of [[], [{ ...schema[0], type: "short_text" }, schema[1]]]) expect((await f.request("PATCH", `${definitions}/${definition.id}`, { ...body, field_schema, expected_version: 1 })).status).toBe(400);
  expect((await f.request("PATCH", `${definitions}/${definition.id}`, { ...body, expected_version: 2 })).status).toBe(409);
  const graph = await (await f.request("POST", `${root}/roadmaps`, { name: "Graph" })).json(), path = `${root}/roadmaps/${graph.roadmap.id}/nodes`;
  const node = { title: "Custom", node_kind: "custom", definition_id: definition.id, expected_version: 1 };
  for (const field_values of [{ score: "5" }, { unknown: true }, { choice: "B" }, { score: null }, [], null]) expect((await f.request("POST", path, { ...node, field_values })).status).toBe(400);
  expect((await f.request("POST", path, { ...node, position_x: 10000001 })).status).toBe(400);
  const created = await f.request("POST", path, { ...node, field_values: { score: 5, choice: "" } }); expect(created.status).toBe(201); const { node: item } = await created.json();
  expect((await f.request("PATCH", `${path}/${item.id}`, { title: "Changed kind", node_kind: "note", expected_version: 2 })).status).toBe(400);
  expect((await f.request("PATCH", `${path}/${item.id}`, { ...node, node_kind: " custom ", expected_version: 2 })).status).toBe(400);
  expect((await f.request("DELETE", `${path}/${item.id}?expected_version=1`)).status).toBe(409);
  expect((await admin.query("SELECT graph_version FROM space_roadmaps WHERE id=$1", [graph.roadmap.id])).rows[0].graph_version).toBe("2");
});

it("enforces definition scopes and node references, and rolls back failed node events", async () => {
  const f = await fixture(), root = `/spaces/${f.spaceId}`, body = { name: "Custom", icon: "shapes", color: "slate", field_schema: [] };
  const readOnly = await f.appToken(["roadmaps.read"]);
  expect((await f.request("GET", `/api${root}/roadmap-node-definitions`, undefined, readOnly)).status).toBe(200);
  expect((await f.request("POST", `/v1${root}/roadmap-node-definitions`, body, readOnly)).status).toBe(403);
  expect((await f.request("POST", `${root}/roadmap-node-definitions`, body, f.outsider.token)).status).toBe(403);
  const definition = await (await f.request("POST", `${root}/roadmap-node-definitions`, body)).json();
  const graph = await (await f.request("POST", `${root}/roadmaps`, { name: "Graph" })).json(), other = await (await f.request("POST", `${root}/roadmaps`, { name: "Other" })).json();
  const path = `${root}/roadmaps/${graph.roadmap.id}/nodes`, node = { title: "Node", node_kind: "custom", definition_id: definition.id, expected_version: 1 };
  expect((await f.request("POST", path, { ...node, milestone_id: other.milestones[0].id })).status).toBe(404);
  const otherSpace = (await createSpaceRepository(application).create(f.owner.user.id, { name: "Other Space", template_id: "blank", integration_providers: [] }, "")).space.id;
  const foreign = await (await f.request("POST", `/spaces/${otherSpace}/roadmap-node-definitions`, body)).json();
  expect((await f.request("POST", path, { ...node, definition_id: foreign.id })).status).toBe(404);
  await admin.query("REVOKE INSERT ON space_events FROM misty_roadmap_test");
  try { expect((await f.request("POST", path, node)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_roadmap_test"); }
  expect((await admin.query("SELECT id FROM space_roadmap_nodes WHERE roadmap_id=$1", [graph.roadmap.id])).rowCount).toBe(0);
  expect((await admin.query("SELECT graph_version FROM space_roadmaps WHERE id=$1", [graph.roadmap.id])).rows[0].graph_version).toBe("1");
  expect((await f.request("POST", path, node)).status).toBe(201);
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" }), runtime = createAppRuntimeRepository(application);
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    planner: { auth, appRuntime: runtime, tasks: createTaskRepository(application), roadmaps: createRoadmapRepository(application) }, appRuntime: { repository: runtime } });
  const account = async () => {
    const username = `roadmap_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const value = await auth.register({ username, email: `${username}@example.invalid`, name: "Roadmap test", password: "test-password", analyticsEnabled: false }); users.push(value.user.id); return value;
  };
  const owner = await account(), member = await account(), outsider = await account();
  const { space } = await createSpaceRepository(application).create(owner.user.id, { name: "Roadmaps", template_id: "blank", integration_providers: [] }, "");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [space.id, member.user.id]);
  const request = (method: string, path: string, body?: unknown, token = owner.token) => app.request(path, { method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  const appToken = async (scopes = ["roadmaps.read", "roadmaps.write"]) => {
    const installs = createInstallationRepository(application); await installs.install(member.user.id, { ...createOfficialCatalog().find("terminal")!, scopes });
    const token = randomUUID(); await installs.session(member.user.id, "terminal", hashToken(token), space.id); return token;
  };
  const rpc = (method: string, params: unknown, token: string) => request("POST", "/v1/app-runtime/rpc", { protocol: 2, method, params }, token);
  return { owner, member, outsider, spaceId: space.id, request, appToken, rpc };
}

it("serves Roadmap CRUD, seed milestone and version conflicts through the existing five SDK methods", async () => {
  const f = await fixture(), token = await f.appToken();
  const created = await f.rpc("roadmaps.create", { body: { name: "  Release  ", description: " Plan " } }, token);
  expect(created.status, await created.clone().text()).toBe(201);
  const snapshot = await created.json(); expect(() => parseMethodResult("roadmaps.create", snapshot)).not.toThrow();
  const id = snapshot.roadmap.id;
  expect(snapshot.roadmap).toMatchObject({ name: "Release", description: "Plan", graph_version: 1, created_by_user_id: f.member.user.id, audience_kind: "space" });
  expect(snapshot.milestones).toHaveLength(1); expect(snapshot.milestones[0]).toMatchObject({ title: "First milestone", rank: 1024, position_x: 80, position_y: 80, width: 440, height: 360, status: "not_started", goal_total: 0 });
  expect(snapshot).toMatchObject({ goals: [], nodes: [], node_definitions: [], edges: [], goal_total: 0, progress_percentage: 0 });
  const list = await (await f.rpc("roadmaps.list", {}, token)).json(); expect(() => parseMethodResult("roadmaps.list", list)).not.toThrow(); expect(list.roadmaps[0].id).toBe(id);
  const graph = await (await f.rpc("roadmaps.get", { path: { roadmapID: id } }, token)).json(); expect(() => parseMethodResult("roadmaps.get", graph)).not.toThrow(); expect(graph).toEqual(snapshot);
  const updated = await f.rpc("roadmaps.update", { path: { roadmapID: id }, body: { name: "Updated", expected_version: 1 } }, token);
  expect(updated.status).toBe(200); const result = await updated.json(); expect(() => parseMethodResult("roadmaps.update", result)).not.toThrow(); expect(result.graph_version).toBe(2);
  expect((await f.rpc("roadmaps.update", { path: { roadmapID: id }, body: { name: "Stale", expected_version: 1 } }, token)).status).toBe(409);
  const archived = await f.rpc("roadmaps.delete", { path: { roadmapID: id }, query: { expected_version: 2 } }, token); expect(archived.status).toBe(200);
  const archiveResult = await archived.json(); expect(() => parseMethodResult("roadmaps.delete", archiveResult)).not.toThrow(); expect(archiveResult).toEqual({ graph_version: 3 });
  expect((await f.rpc("roadmaps.get", { path: { roadmapID: id } }, token)).status).toBe(404);
  expect((await f.rpc("roadmaps.delete", { path: { roadmapID: id }, query: { expected_version: 3 } }, token)).status).toBe(404);
  expect((await admin.query("SELECT event_type FROM space_events WHERE entity_id=$1 ORDER BY id", [id])).rows.map(row => row.event_type)).toEqual(["roadmap.created", "roadmap.updated", "roadmap.archived"]);
});

it("loads complete graph data and computes progress from visible linked tasks", async () => {
  const f = await fixture();
  const snapshot = await (await f.request("POST", `/spaces/${f.spaceId}/roadmaps`, { name: "Graph" })).json();
  const roadmap = snapshot.roadmap.id, milestone = snapshot.milestones[0].id, goals = [randomUUID(), randomUUID()], definition = randomUUID(), unusedDefinition = randomUUID(), node = randomUUID(), hidden = randomUUID();
  for (let index = 0; index < goals.length; index++) await admin.query(`INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,rank,target_date,manual_completed_at,manual_completed_by_user_id)
    VALUES($1,$2,$3,$4,$5,$6,'2026-09-09',CASE WHEN $7 THEN now() END,CASE WHEN $7 THEN $8::text END)`, [goals[index], f.spaceId, roadmap, milestone, index ? "Manual" : "Linked", (index + 1) * 1024, index === 1, f.owner.user.id]);
  const conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,created_by_user_id,title) VALUES($1,$2,$3,'Private')", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES($1,$2,'person')", [conversation, f.owner.user.id]);
  for (const [index, status] of ["done", "in_progress", "canceled", "done", "done"].entries()) {
    const task = randomUUID();
    await admin.query(`INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,rank,created_by_user_id,archived_at,audience_kind,audience_conversation_id,audience_creator_user_id)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,CASE WHEN $9 THEN now() END,$10,$11,CASE WHEN $10='conversation' THEN $8::text END)`, [task, f.spaceId, index + 1, `MST-${index + 1}`, index === 4 ? "Private task" : `Task ${index}`, status, (index + 1) * 1024, f.owner.user.id, index === 3, index === 4 ? "conversation" : "space", index === 4 ? conversation : null]);
    await admin.query("INSERT INTO space_roadmap_goal_tasks(space_id,roadmap_id,goal_id,task_id,added_by_user_id) VALUES($1,$2,$3,$4,$5)", [f.spaceId, roadmap, goals[0], task, f.owner.user.id]);
  }
  for (const id of [definition, unusedDefinition]) await admin.query("INSERT INTO space_roadmap_node_definitions(id,space_id,name,created_by_user_id,archived_at) VALUES($1,$2,$1,$3,now())", [id, f.spaceId, f.owner.user.id]);
  for (const id of [node, hidden]) await admin.query("INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,node_kind,definition_id,title,archived_at) VALUES($1,$2,$3,'custom',$4,$1,CASE WHEN $5 THEN now() END)", [id, f.spaceId, roadmap, definition, id === hidden]);
  for (const target of [node, hidden]) await admin.query("INSERT INTO space_roadmap_edges(id,space_id,roadmap_id,source_kind,source_id,target_kind,target_id,edge_type,label) VALUES($1,$2,$3,'goal',$4,'node',$5,'related','Link')", [randomUUID(), f.spaceId, roadmap, goals[0], target]);
  const read = async (token: string) => {
    const response = await f.request("GET", `/api/spaces/${f.spaceId}/roadmaps/${roadmap}`, undefined, token); expect(response.status, await response.clone().text()).toBe(200);
    const result = await response.json(); expect(() => parseMethodResult("roadmaps.get", result)).not.toThrow(); return result;
  };
  const graph = await read(f.member.token);
  expect(graph).toMatchObject({ goal_total: 2, goal_done: 1, progress_percentage: 50, milestone_done: 0 });
  expect(graph.goals[0]).toMatchObject({ title: "Linked", task_total: 2, task_done: 1, progress_percentage: 50, status: "in_progress", target_date: "2026-09-09T00:00:00.000Z" });
  expect(graph.goals[0].tasks).toHaveLength(4); expect(graph.goals[1].status).toBe("done");
  expect(graph.milestones[0]).toMatchObject({ goal_total: 2, goal_done: 1, status: "in_progress" });
  expect(graph.nodes.map((item: { id: string }) => item.id)).toEqual([node]); expect(graph.node_definitions.map((item: { id: string }) => item.id)).toEqual([definition]);
  expect(graph.edges).toHaveLength(1); expect(graph.edges[0]).toMatchObject({ source: { kind: "goal", id: goals[0] }, target: { kind: "node", id: node } });
  expect(JSON.stringify(graph)).not.toContain("Private task"); expect((await read(f.owner.token)).goals[0]).toMatchObject({ task_total: 3, task_done: 2, progress_percentage: 67 });
});

it("enforces Roadmap scopes, current membership and private audience on reads and writes", async () => {
  const f = await fixture(), token = await f.appToken(["roadmaps.write"]);
  const created = await f.request("POST", `/v1/spaces/${f.spaceId}/roadmaps`, { name: "Private", created_by_user_id: f.outsider.user.id, audience_kind: "conversation" }, token);
  expect(created.status).toBe(201); const snapshot = await created.json(), id = snapshot.roadmap.id;
  expect(snapshot.roadmap).toMatchObject({ audience_kind: "space", created_by_user_id: f.member.user.id });
  expect((await f.request("GET", `/spaces/${f.spaceId}/roadmaps/${id}`, undefined, token)).status).toBe(403);
  const conversation = randomUUID(); await admin.query("INSERT INTO space_conversations(id,space_id,created_by_user_id,title) VALUES($1,$2,$3,'Private')", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES($1,$2,'person')", [conversation, f.owner.user.id]);
  await admin.query("UPDATE space_roadmaps SET audience_kind='conversation',audience_conversation_id=$2 WHERE id=$1", [id, conversation]);
  for (const prefix of ["", "/api", "/v1"]) {
    const path = `${prefix}/spaces/${f.spaceId}/roadmaps/${id}`;
    expect((await f.request("GET", path)).status).toBe(200);
    expect((await f.request("GET", path, undefined, f.member.token)).status).toBe(404);
    expect((await f.request("PATCH", path, { name: "Forbidden", expected_version: 1 }, f.member.token)).status).toBe(404);
    expect((await f.request("DELETE", `${path}?expected_version=1`, undefined, f.member.token)).status).toBe(404);
  }
  expect((await f.request("GET", `/spaces/${f.spaceId}/roadmaps`, undefined, f.outsider.token)).status).toBe(403);
  expect((await (await f.request("GET", `/spaces/${f.spaceId}/roadmaps`, undefined, f.member.token)).json()).roadmaps).toEqual([]);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'tasks.view','deny',$2)", [f.spaceId, f.member.user.id]);
  expect((await f.request("GET", `/spaces/${f.spaceId}/roadmaps/${id}`, undefined, f.member.token)).status).toBe(403);
});

it("validates Roadmap input, serializes competing versions and rolls back failed event recording", async () => {
  const f = await fixture(), path = `/spaces/${f.spaceId}/roadmaps`;
  for (const name of [" ", "x".repeat(161)]) expect((await f.request("POST", path, { name })).status).toBe(400);
  await admin.query("REVOKE INSERT ON space_events FROM misty_roadmap_test");
  try { expect((await f.request("POST", path, { name: "Rollback" })).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_roadmap_test"); }
  expect((await admin.query("SELECT id FROM space_roadmaps WHERE space_id=$1", [f.spaceId])).rows).toHaveLength(0);
  expect((await admin.query("SELECT id FROM space_roadmap_milestones WHERE space_id=$1", [f.spaceId])).rows).toHaveLength(0);
  const created = await f.request("POST", path, { name: "😀".repeat(160) }); expect(created.status).toBe(201); const { roadmap } = await created.json();
  const edits = await Promise.all(["First", "Second"].map(name => f.request("PATCH", `${path}/${roadmap.id}`, { name, expected_version: 1 })));
  expect(edits.map(response => response.status).sort()).toEqual([200, 409]);
  expect((await f.request("DELETE", `${path}/${roadmap.id}?expected_version=0`)).status).toBe(400);
  expect((await f.request("PATCH", `${path}/${roadmap.id}`, { name: "No version" })).status).toBe(400);
});

it("executes all seven milestone, goal and task-link SDK mutations with graph versions", async () => {
  const f = await fixture(), token = await f.appToken(), snapshot = await (await f.rpc("roadmaps.create", { body: { name: "Nested" } }, token)).json(), roadmapID = snapshot.roadmap.id;
  let version = 1;
  const write = async (method: string, path: Record<string, string>, body?: Record<string, unknown>) => {
    const response = await f.rpc(method, { path: { roadmapID, ...path }, ...(body ? { body: { ...body, expected_version: version } } : { query: { expected_version: version } }) }, token);
    expect(response.status, await response.clone().text()).toBe(method.endsWith(".create") ? 201 : 200);
    const result = await response.json(); expect(() => parseMethodResult(method as Parameters<typeof parseMethodResult>[0], result)).not.toThrow();
    expect(result.graph_version).toBe(++version); return result;
  };
  const { milestone } = await write("roadmaps.milestones.create", {}, { title: "  Launch  ", target_date: "2026-09-10T23:00:00-07:00", position_x: 41, position_y: 42, width: 500, height: 400, rank: 9999 });
  expect(milestone).toMatchObject({ title: "Launch", rank: 2048, position_x: 41, position_y: 42, target_date: "2026-09-10T00:00:00.000Z", goal_total: 0, status: "" });
  const edited = await write("roadmaps.milestones.update", { milestoneID: milestone.id }, { title: "Renamed", rank: 4096, position_x: 999, width: 999 });
  expect(edited.milestone).toMatchObject({ rank: 4096, position_x: 41, width: 500, version: 2 });
  const { goal } = await write("roadmaps.goals.create", {}, { title: "Ship", milestone_id: milestone.id });
  expect(goal).toMatchObject({ position_x: 0, position_y: 0, rank: 1024, tasks: [], task_total: 0, status: "" });
  const completed = await write("roadmaps.goals.update", { goalID: goal.id }, { title: "Ship", milestone_id: milestone.id, complete_manually: true, manual_completed_by_user_id: f.outsider.user.id });
  expect(completed.goal.manual_completed_by_user_id).toBe(f.member.user.id);
  const renamed = await write("roadmaps.goals.update", { goalID: goal.id }, { title: "Ship renamed", milestone_id: milestone.id, manual_completed_by_user_id: f.outsider.user.id });
  expect(renamed.goal.manual_completed_by_user_id).toBe(f.member.user.id); expect(renamed.goal.manual_completed_at).toBe(completed.goal.manual_completed_at);
  const task = randomUUID();
  await admin.query("INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,rank,created_by_user_id) VALUES($1,$2,1,'MST-1','Task','done',1024,$3)", [task, f.spaceId, f.owner.user.id]);
  await write("roadmaps.goals.setTasks", { goalID: goal.id }, { task_ids: [task] });
  const linked = await (await f.rpc("roadmaps.get", { path: { roadmapID } }, token)).json();
  expect(linked.goals[0]).toMatchObject({ task_total: 1, task_done: 1, status: "done", progress_percentage: 100 }); expect(linked.goals[0]).not.toHaveProperty("manual_completed_at");
  expect((await f.rpc("roadmaps.goals.update", { path: { roadmapID, goalID: goal.id }, body: { title: "Blocked manual", milestone_id: milestone.id, complete_manually: true, expected_version: version } }, token)).status).toBe(400);
  await write("roadmaps.goals.delete", { goalID: goal.id }); await write("roadmaps.milestones.delete", { milestoneID: milestone.id });
  const final = await (await f.rpc("roadmaps.get", { path: { roadmapID } }, token)).json(); expect(final.goals).toEqual([]); expect(final.milestones).toHaveLength(1); expect(final.roadmap.graph_version).toBe(version);
});

it("rejects inaccessible task links and stale or cross-graph targets without changing existing links", async () => {
  const f = await fixture(), token = await f.appToken(), create = async () => (await f.request("POST", `/spaces/${f.spaceId}/roadmaps`, { name: "Graph" })).json();
  const graph = await create(), other = await create(), roadmapId = graph.roadmap.id, milestone = graph.milestones[0].id;
  const goalResponse = await f.request("POST", `/api/spaces/${f.spaceId}/roadmaps/${roadmapId}/goals`, { title: "Goal", milestone_id: milestone, expected_version: 1 }); expect(goalResponse.status).toBe(201); const { goal } = await goalResponse.json();
  const conversation = randomUUID(), task = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  await admin.query(`INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,rank,created_by_user_id,audience_kind,audience_conversation_id,audience_creator_user_id)
    VALUES($1,$2,1,'MST-1','Private','todo',1024,$3,'conversation',$4,$3)`, [task, f.spaceId, f.owner.user.id, conversation]);
  const path = `/v1/spaces/${f.spaceId}/roadmaps/${roadmapId}/goals/${goal.id}`;
  expect((await f.request("PUT", `${path}/tasks`, { task_ids: [task], expected_version: 2 }, token)).status).toBe(400);
  expect((await f.request("PUT", `${path}/tasks`, { task_ids: [task, ` ${task} `], expected_version: 2 })).status).toBe(400);
  expect((await f.request("PATCH", path, { title: "Cross", milestone_id: other.milestones[0].id, expected_version: 2 })).status).toBe(404);
  expect((await f.request("PUT", `${path}/tasks`, { task_ids: [task], expected_version: 2 })).status).toBe(200);
  expect((await f.request("PUT", `${path}/tasks`, { task_ids: [], expected_version: 2 })).status).toBe(409);
  expect((await admin.query("SELECT task_id FROM space_roadmap_goal_tasks WHERE goal_id=$1", [goal.id])).rows).toEqual([{ task_id: task }]);
  expect((await admin.query("SELECT graph_version FROM space_roadmaps WHERE id=$1", [roadmapId])).rows[0].graph_version).toBe("3");
  const readOnly = await f.appToken(["roadmaps.read"]); expect((await f.request("DELETE", `${path}?expected_version=3`, undefined, readOnly)).status).toBe(403);
});

it("archives milestone children and edges atomically while retaining linked tasks", async () => {
  const f = await fixture(), snapshot = await (await f.request("POST", `/spaces/${f.spaceId}/roadmaps`, { name: "Archive" })).json(), roadmap = snapshot.roadmap.id, milestone = snapshot.milestones[0].id;
  const goal = randomUUID(), node = randomUUID(), edge = randomUUID(), task = randomUUID();
  await admin.query("INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,rank) VALUES($1,$2,$3,$4,'Goal',1024)", [goal, f.spaceId, roadmap, milestone]);
  await admin.query("INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,milestone_id,node_kind,title) VALUES($1,$2,$3,$4,'note','Node')", [node, f.spaceId, roadmap, milestone]);
  await admin.query("INSERT INTO space_roadmap_edges(id,space_id,roadmap_id,source_kind,source_id,target_kind,target_id,edge_type,label) VALUES($1,$2,$3,'milestone',$4,'node',$5,'related','Link')", [edge, f.spaceId, roadmap, milestone, node]);
  await admin.query("INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,rank,created_by_user_id) VALUES($1,$2,1,'MST-1','Keep','todo',1024,$3)", [task, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_roadmap_goal_tasks(space_id,roadmap_id,goal_id,task_id,added_by_user_id) VALUES($1,$2,$3,$4,$5)", [f.spaceId, roadmap, goal, task, f.owner.user.id]);
  const path = `/spaces/${f.spaceId}/roadmaps/${roadmap}/milestones/${milestone}?expected_version=1`;
  await admin.query("REVOKE INSERT ON space_events FROM misty_roadmap_test");
  try { expect((await f.request("DELETE", path)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_events TO misty_roadmap_test"); }
  expect((await admin.query("SELECT archived_at FROM space_roadmap_goals WHERE id=$1", [goal])).rows[0].archived_at).toBeNull();
  expect((await admin.query("SELECT id FROM space_roadmap_edges WHERE id=$1", [edge])).rows).toHaveLength(1);
  expect((await f.request("DELETE", path)).status).toBe(200);
  expect((await admin.query("SELECT archived_at FROM space_roadmap_goals WHERE id=$1", [goal])).rows[0].archived_at).toBeInstanceOf(Date);
  expect((await admin.query("SELECT archived_at FROM space_roadmap_nodes WHERE id=$1", [node])).rows[0].archived_at).toBeInstanceOf(Date);
  expect((await admin.query("SELECT id FROM space_roadmap_edges WHERE id=$1", [edge])).rows).toHaveLength(0);
  expect((await admin.query("SELECT archived_at FROM space_tasks WHERE id=$1", [task])).rows[0].archived_at).toBeNull();
  expect((await admin.query("SELECT task_id FROM space_roadmap_goal_tasks WHERE goal_id=$1", [goal])).rows).toHaveLength(1);
});
