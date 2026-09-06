import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { z } from "zod";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError, trimSpace } from "../../spaces/model.js";
import { roadmapResponse, roadmapVersion, type RoadmapRow } from "./model.js";
import { roadmapChildNotification, type RoadmapTransactions } from "./transactions.js";

const id = z.string().nullable().optional().transform(value => value ?? "");
const endpoint = z.object({ kind: id, id });
const causal = new Set(["depends_on", "dependency", "blocks", "enables"]);
const columns = "id,space_id,roadmap_id,source_kind,source_id,target_kind,target_id,source_goal_id,target_goal_id,edge_type,label,version,created_at,updated_at";
type Endpoint = z.infer<typeof endpoint>;
type EdgeRow = RoadmapRow & { source_kind: string; source_id: string; target_kind: string; target_id: string };
export function edgeResponse({ source_kind, source_id, target_kind, target_id, ...row }: EdgeRow) {
  return { ...roadmapResponse(row), source: { kind: source_kind, id: source_id }, target: { kind: target_kind, id: target_id } };
}
function edgeInput(raw: unknown) {
  const parsed = z.object({ source: endpoint.nullable().optional(), target: endpoint.nullable().optional(), source_goal_id: id, target_goal_id: id,
    edge_type: id, label: id, expected_version: z.number().int().safe().positive() }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request");
  const value = parsed.data, source = value.source?.id ? value.source : { kind: "goal", id: value.source_goal_id }, target = value.target?.id ? value.target : { kind: "goal", id: value.target_goal_id };
  const label = trimSpace(value.label), type = trimSpace(value.edge_type);
  if (!source.id || !target.id || source.kind === target.kind && source.id === target.id || [...label].length > 120) throw new SpaceError("invalid_request");
  return { source, target, label, edge_type: type === "dependency" ? "depends_on" : type, expected_version: value.expected_version };
}
async function endpointKind(tx: PoolClient, spaceId: string, roadmapId: string, value: Endpoint) {
  const table = value.kind === "goal" ? "space_roadmap_goals" : value.kind === "milestone" ? "space_roadmap_milestones" : value.kind === "node" ? "space_roadmap_nodes" : null;
  if (!table) throw new SpaceError("not_found");
  const row = (await tx.query<{ kind: string }>(`SELECT ${value.kind === "node" ? "node_kind" : `'${value.kind}'`} AS kind FROM ${table} WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, [value.id, roadmapId, spaceId])).rows[0];
  if (!row) throw new SpaceError("not_found"); return row.kind;
}
async function validateEdge(tx: PoolClient, spaceId: string, roadmapId: string, excludedId: string, input: ReturnType<typeof edgeInput>) {
  const sourceType = await endpointKind(tx, spaceId, roadmapId, input.source);
  await endpointKind(tx, spaceId, roadmapId, input.target);
  const sourceGoal = input.source.kind === "goal", targetGoal = input.target.kind === "goal", targetWork = targetGoal || input.target.kind === "milestone";
  const valid = input.edge_type === "depends_on" ? sourceGoal && targetGoal
    : input.edge_type === "blocks" ? (sourceGoal || sourceType === "risk") && targetWork
    : input.edge_type === "enables" ? (sourceGoal || sourceType === "decision") && targetWork
    : input.edge_type === "contributes_to" ? input.source.kind === "node" && targetWork
    : input.edge_type === "measures" ? sourceType === "metric" && targetWork
    : input.edge_type === "documents" ? sourceType === "note" : input.edge_type === "related";
  if (!valid) throw new SpaceError("invalid_request");
  if (causal.has(input.edge_type) && sourceGoal && targetGoal) {
    const edges = (await tx.query<{ source_id: string; target_id: string }>("SELECT source_id,target_id FROM space_roadmap_edges WHERE roadmap_id=$1 AND space_id=$2 AND source_kind='goal' AND target_kind='goal' AND edge_type IN ('dependency','depends_on','blocks','enables') AND id<>$3", [roadmapId, spaceId, excludedId])).rows;
    const graph = new Map<string, string[]>();
    for (const edge of edges) { const targets = graph.get(edge.source_id) ?? []; targets.push(edge.target_id); graph.set(edge.source_id, targets); }
    const seen = new Set<string>(), stack = [input.target.id];
    while (stack.length) {
      const current = stack.pop()!;
      if (current === input.source.id) throw new SpaceError("invalid_request");
      if (!seen.has(current)) { seen.add(current); stack.push(...graph.get(current) ?? []); }
    }
  }
}
export function createEdgeRepository({ mutate }: RoadmapTransactions) {
  const save = (actor: SpaceActor, spaceId: string, roadmapId: string, raw: unknown, existingId?: string) => {
    const input = edgeInput(raw), id = existingId ?? `edge_${randomUUID()}`;
    return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
      await validateEdge(tx, spaceId, roadmapId, id, input);
      const values = [id, spaceId, roadmapId, input.source.kind === "goal" ? input.source.id : null, input.target.kind === "goal" ? input.target.id : null,
        input.source.kind, input.source.id, input.target.kind, input.target.id, input.edge_type, input.label];
      const row = (await tx.query<EdgeRow>(existingId === undefined
        ? `INSERT INTO space_roadmap_edges(id,space_id,roadmap_id,source_goal_id,target_goal_id,source_kind,source_id,target_kind,target_id,edge_type,label) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING ${columns}`
        : `UPDATE space_roadmap_edges SET source_goal_id=$4,target_goal_id=$5,source_kind=$6,source_id=$7,target_kind=$8,target_id=$9,edge_type=$10,label=$11,version=version+1,updated_at=now() WHERE id=$1 AND space_id=$2 AND roadmap_id=$3 RETURNING ${columns}`, values)).rows[0];
      if (!row) throw new SpaceError("not_found");
      await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "edge.updated", version); return { edge: edgeResponse(row), graph_version: version };
    });
  };
  return {
    create: (actor: SpaceActor, spaceId: string, roadmapId: string, raw: unknown) => save(actor, spaceId, roadmapId, raw),
    update: (actor: SpaceActor, spaceId: string, roadmapId: string, id: string, raw: unknown) => save(actor, spaceId, roadmapId, raw, id),
    delete(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, expected: string) {
      roadmapVersion(expected);
      return mutate(actor, spaceId, roadmapId, expected, async (tx, version) => {
        if (!(await tx.query("DELETE FROM space_roadmap_edges WHERE id=$1 AND roadmap_id=$2 AND space_id=$3", [id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "edge.removed", version); return { graph_version: version };
      });
    },
  };
}
