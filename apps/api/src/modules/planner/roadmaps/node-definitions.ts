import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { roadmapResponse, roadmapVersion, type RoadmapRow } from "./model.js";
import { definitionColumns, definitionInput, validateDefinitionUpdate } from "./node-model.js";
import type { RoadmapTransactions } from "./transactions.js";

async function notify(tx: PoolClient, actor: SpaceActor, spaceId: string, id: string, kind: string, version?: number) {
  const event = (await tx.query<{ id: string }>("INSERT INTO space_events(space_id,actor_user_id,event_type,entity_id,payload) VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id",
    [spaceId, actor.userId, `roadmap.node_definition.${kind}`, id, JSON.stringify({ definition_id: id, ...(version === undefined ? {} : { definition_version: version }) })])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
}
export function createNodeDefinitionRepository({ transaction }: RoadmapTransactions) {
  return {
    list(actor: SpaceActor, spaceId: string) {
      return transaction(actor, spaceId, false, async tx => ({ node_definitions: (await tx.query<RoadmapRow>(`SELECT ${definitionColumns} FROM space_roadmap_node_definitions WHERE space_id=$1 AND archived_at IS NULL ORDER BY name,id`, [spaceId])).rows.map(roadmapResponse) }));
    },
    create(actor: SpaceActor, spaceId: string, raw: unknown) {
      const input = definitionInput(raw), id = `roadmap_node_definition_${randomUUID()}`;
      return transaction(actor, spaceId, true, async tx => {
        const row = (await tx.query<RoadmapRow>(`INSERT INTO space_roadmap_node_definitions(id,space_id,name,description,icon,color,agenda_visible,field_schema,created_by_user_id)
          VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING ${definitionColumns}`, [id, spaceId, input.name, input.description, input.icon, input.color, input.agenda_visible, JSON.stringify(input.field_schema), actor.userId])).rows[0]!;
        await notify(tx, actor, spaceId, id, "created"); return roadmapResponse(row);
      });
    },
    update(actor: SpaceActor, spaceId: string, id: string, raw: unknown) {
      const input = definitionInput(raw, true);
      return transaction(actor, spaceId, true, async tx => {
        const previous = (await tx.query<{ field_schema: unknown }>("SELECT field_schema FROM space_roadmap_node_definitions WHERE id=$1 AND space_id=$2 AND archived_at IS NULL FOR UPDATE", [id, spaceId])).rows[0];
        if (!previous) throw new SpaceError("not_found");
        validateDefinitionUpdate(previous.field_schema, input.fields);
        const row = (await tx.query<RoadmapRow>(`UPDATE space_roadmap_node_definitions SET name=$3,description=$4,icon=$5,color=$6,agenda_visible=$7,field_schema=$8::jsonb,version=version+1,updated_at=now()
          WHERE id=$1 AND space_id=$2 AND archived_at IS NULL AND version=$9 RETURNING ${definitionColumns}`, [id, spaceId, input.name, input.description, input.icon, input.color, input.agenda_visible, JSON.stringify(input.field_schema), input.expected_version])).rows[0];
        if (!row) throw new SpaceError("version_conflict");
        const result = roadmapResponse(row); await notify(tx, actor, spaceId, id, "updated", result.version as number); return result;
      });
    },
    archive(actor: SpaceActor, spaceId: string, id: string, expected: string) {
      roadmapVersion(expected);
      return transaction(actor, spaceId, true, async tx => {
        if (!(await tx.query("UPDATE space_roadmap_node_definitions SET archived_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND space_id=$2 AND archived_at IS NULL AND version=$3", [id, spaceId, expected])).rowCount) throw new SpaceError("version_conflict");
        await notify(tx, actor, spaceId, id, "archived");
      });
    },
  };
}
