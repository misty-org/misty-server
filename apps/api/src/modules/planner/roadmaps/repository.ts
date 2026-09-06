import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import type { SpaceActor } from "../../spaces/access.js";
import { clientInteger } from "../../spaces/model.js";
import { createRoadmapTransactions, lockRoadmap, roadmapNotification } from "./transactions.js";
import { roadmapAudience, roadmapColumns, roadmapInput, roadmapResponse, roadmapVersion, type RoadmapRow } from "./model.js";
import { createMilestoneRepository } from "./milestones.js";
import { createGoalRepository } from "./goals.js";
import { createNodeDefinitionRepository } from "./node-definitions.js";
import { createNodeRepository } from "./nodes.js";
import { createEdgeRepository } from "./edges.js";
import { createLayoutRepository } from "./layout.js";
import { loadRoadmapSnapshot } from "./snapshot.js";

export function createRoadmapRepository(pool: Pool) {
  const transactions = createRoadmapTransactions(pool), { transaction } = transactions;
  return {
    transaction, milestones: createMilestoneRepository(transactions), goals: createGoalRepository(transactions),
    definitions: createNodeDefinitionRepository(transactions), nodes: createNodeRepository(transactions),
    edges: createEdgeRepository(transactions), layout: createLayoutRepository(transactions),
    list(actor: SpaceActor, spaceId: string) {
      return transaction(actor, spaceId, false, async tx => ({ roadmaps: (await tx.query<RoadmapRow>(`SELECT ${roadmapColumns} FROM space_roadmaps r
        WHERE r.space_id=$1 AND r.archived_at IS NULL AND ${roadmapAudience("$2")} ORDER BY r.updated_at DESC,r.id`, [spaceId, actor.userId])).rows.map(roadmapResponse) }));
    },
    get(actor: SpaceActor, spaceId: string, id: string) { return transaction(actor, spaceId, false, tx => loadRoadmapSnapshot(tx, actor.userId, spaceId, id)); },
    create(actor: SpaceActor, spaceId: string, raw: unknown) {
      const input = roadmapInput(raw), id = `roadmap_${randomUUID()}`;
      return transaction(actor, spaceId, true, async tx => {
        // The public Go handler creates Space-visible roadmaps; body audience and creator fields are not authoritative.
        await tx.query("INSERT INTO space_roadmaps(id,space_id,name,description,created_by_user_id) VALUES($1,$2,$3,$4,$5)", [id, spaceId, input.name, input.description, actor.userId]);
        await tx.query("INSERT INTO space_roadmap_milestones(id,space_id,roadmap_id,title,rank,position_x,position_y) VALUES($1,$2,$3,'First milestone',1024,80,80)", [`milestone_${randomUUID()}`, spaceId, id]);
        await roadmapNotification(tx, actor, spaceId, id, "created"); return loadRoadmapSnapshot(tx, actor.userId, spaceId, id);
      });
    },
    update(actor: SpaceActor, spaceId: string, id: string, raw: unknown) {
      const input = roadmapInput(raw, true);
      return transaction(actor, spaceId, true, async tx => {
        await lockRoadmap(tx, actor, spaceId, id, input.expected_version!);
        const row = (await tx.query<RoadmapRow>(`UPDATE space_roadmaps r SET name=$3,description=$4,graph_version=r.graph_version+1,updated_at=now()
          WHERE r.id=$1 AND r.space_id=$2 RETURNING ${roadmapColumns}`, [id, spaceId, input.name, input.description])).rows[0]!;
        const result = roadmapResponse(row); await roadmapNotification(tx, actor, spaceId, id, "updated", result.graph_version as number); return result;
      });
    },
    archive(actor: SpaceActor, spaceId: string, id: string, expected: string) {
      roadmapVersion(expected);
      return transaction(actor, spaceId, true, async tx => {
        await lockRoadmap(tx, actor, spaceId, id, expected);
        const row = (await tx.query<{ graph_version: string }>("UPDATE space_roadmaps SET archived_at=now(),graph_version=graph_version+1,updated_at=now() WHERE id=$1 AND space_id=$2 RETURNING graph_version", [id, spaceId])).rows[0]!;
        const version = clientInteger(row.graph_version); await roadmapNotification(tx, actor, spaceId, id, "archived", version); return { graph_version: version };
      });
    },
  };
}
