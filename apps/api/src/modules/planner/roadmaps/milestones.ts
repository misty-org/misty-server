import { randomUUID } from "node:crypto";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { roadmapVersion, type RoadmapRow } from "./model.js";
import { milestoneColumns, milestoneInput, milestoneResponse } from "./child-model.js";
import { roadmapChildNotification, type RoadmapTransactions } from "./transactions.js";
export function createMilestoneRepository({ mutate }: RoadmapTransactions) {
  return {
    create(actor: SpaceActor, spaceId: string, roadmapId: string, raw: unknown) {
      const input = milestoneInput(raw, true), id = `milestone_${randomUUID()}`;
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        const row = (await tx.query<RoadmapRow>(`INSERT INTO space_roadmap_milestones(id,space_id,roadmap_id,title,description,target_date,rank,position_x,position_y,width,height)
          VALUES($1,$2,$3,$4,$5,$6,(SELECT COALESCE(MAX(rank),0)+1024 FROM space_roadmap_milestones WHERE roadmap_id=$3 AND archived_at IS NULL),$7,$8,$9,$10)
          RETURNING ${milestoneColumns}`, [id, spaceId, roadmapId, input.title, input.description, input.target_date, input.position_x, input.position_y, input.width, input.height])).rows[0]!;
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "milestone.created", version); return { milestone: milestoneResponse(row), graph_version: version };
      });
    },
    update(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, raw: unknown) {
      const input = milestoneInput(raw);
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        const row = (await tx.query<RoadmapRow>(`UPDATE space_roadmap_milestones SET title=$4,description=$5,target_date=$6,rank=CASE WHEN $7>0 THEN $7 ELSE rank END,version=version+1,updated_at=now()
          WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL RETURNING ${milestoneColumns}`, [id, roadmapId, spaceId, input.title, input.description, input.target_date, input.rank])).rows[0];
        if (!row) throw new SpaceError("not_found");
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "milestone.updated", version); return { milestone: milestoneResponse(row), graph_version: version };
      });
    },
    archive(actor: SpaceActor, spaceId: string, roadmapId: string, id: string, expected: string) {
      roadmapVersion(expected);
      return mutate(actor, spaceId, roadmapId, expected, async (tx, version) => {
        if (!(await tx.query("UPDATE space_roadmap_milestones SET archived_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        await tx.query("UPDATE space_roadmap_goals SET archived_at=now(),version=version+1,updated_at=now() WHERE milestone_id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId]);
        await tx.query("UPDATE space_roadmap_nodes SET archived_at=now(),version=version+1,updated_at=now() WHERE milestone_id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL", [id, roadmapId, spaceId]);
        await tx.query(`DELETE FROM space_roadmap_edges WHERE roadmap_id=$1 AND space_id=$3 AND
          ((source_kind='milestone' AND source_id=$2) OR (target_kind='milestone' AND target_id=$2)
          OR (source_kind='node' AND source_id IN(SELECT id FROM space_roadmap_nodes WHERE milestone_id=$2))
          OR (target_kind='node' AND target_id IN(SELECT id FROM space_roadmap_nodes WHERE milestone_id=$2)))`, [roadmapId, id, spaceId]);
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, id, "milestone.archived", version); return { graph_version: version };
      });
    },
  };
}
