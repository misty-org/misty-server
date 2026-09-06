import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { roadmapResponse, roadmapVersion, type RoadmapRow } from "./model.js";
import { nodeColumns, nodeInput, validateFieldValues } from "./node-model.js";
import { roadmapChildNotification, type RoadmapTransactions } from "./transactions.js";

async function validateReferences(tx: PoolClient, spaceId: string, roadmapId: string, input: ReturnType<typeof nodeInput>, update: boolean) {
  if (input.milestone_id && !(await tx.query("SELECT id FROM space_roadmap_milestones WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [input.milestone_id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
  if (input.node_kind === "custom") {
    const definition = (await tx.query<{ field_schema: unknown }>(`SELECT field_schema FROM space_roadmap_node_definitions WHERE id=$1 AND space_id=$2 ${update ? "" : "AND archived_at IS NULL"} FOR SHARE`, [input.definition_id, spaceId])).rows[0];
    if (!definition) throw new SpaceError("not_found");
    validateFieldValues(input.field_values, definition.field_schema);
  }
}
export function createNodeRepository({ mutate }: RoadmapTransactions) {
  return {
    create(actor: SpaceActor, spaceId: string, roadmapId: string, raw: unknown) {
      const input = nodeInput(raw), id = `roadmap_node_${randomUUID()}`;
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        await validateReferences(tx, spaceId, roadmapId, input, false);
        const row = (await tx.query<RoadmapRow>(`INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,milestone_id,definition_id,node_kind,title,description,target_date,position_x,position_y,field_values)
          VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12::jsonb) RETURNING ${nodeColumns}`,
        [id, spaceId, roadmapId, input.milestone_id, input.definition_id, input.node_kind, input.title, input.description, input.target_date, input.position_x, input.position_y, JSON.stringify(input.field_values)])).rows[0]!;
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "node.created", version, { node_kind: input.node_kind });
        return { node: roadmapResponse(row), graph_version: version };
      });
    },
    update(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, raw: unknown) {
      const input = nodeInput(raw);
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        const existing = (await tx.query<{ node_kind: string; definition_id: string }>("SELECT node_kind,COALESCE(definition_id,'') AS definition_id FROM space_roadmap_nodes WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId])).rows[0];
        if (!existing) throw new SpaceError("not_found");
        if (existing.node_kind !== input.raw_node_kind || existing.definition_id !== input.definition_id) throw new SpaceError("invalid_request");
        await validateReferences(tx, spaceId, roadmapId, input, true);
        const row = (await tx.query<RoadmapRow>(`UPDATE space_roadmap_nodes SET milestone_id=NULLIF($4,''),title=$5,description=$6,target_date=$7,position_x=$8,position_y=$9,field_values=$10::jsonb,version=version+1,updated_at=now()
          WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL RETURNING ${nodeColumns}`,
        [id, roadmapId, spaceId, input.milestone_id, input.title, input.description, input.target_date, input.position_x, input.position_y, JSON.stringify(input.field_values)])).rows[0]!;
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "node.updated", version, { node_kind: input.node_kind });
        return { node: roadmapResponse(row), graph_version: version };
      });
    },
    archive(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, expected: string) {
      roadmapVersion(expected);
      return mutate(actor, spaceId, roadmapId, expected, async (tx, version) => {
        if (!(await tx.query("UPDATE space_roadmap_nodes SET archived_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        await tx.query("DELETE FROM space_roadmap_edges WHERE roadmap_id=$1 AND space_id=$3 AND ((source_kind='node' AND source_id=$2) OR (target_kind='node' AND target_id=$2))", [roadmapId, id, spaceId]);
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "node.archived", version); return { graph_version: version };
      });
    },
  };
}
