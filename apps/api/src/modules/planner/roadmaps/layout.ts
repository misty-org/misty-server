import { z } from "zod";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import { roadmapChildNotification, type RoadmapTransactions } from "./transactions.js";

const number = z.number().finite().nullable().optional().transform(value => value ?? 0);
const position = number.refine(value => Math.abs(value) <= 10000000);
const item = z.object({ id: z.string().min(1), position_x: position, position_y: position });
const milestone = item.extend({ width: number.refine(value => value >= 280 && value <= 2400), height: number.refine(value => value >= 220 && value <= 2400) });
const goal = item.extend({ milestone_id: z.string().min(1) });
const node = item.extend({ milestone_id: z.string().nullable().optional().transform(value => value ?? "") });
const layoutSchema = z.object({ expected_version: z.number().int().safe().positive(),
  milestones: z.array(milestone).nullable().optional().transform(value => value ?? []),
  goals: z.array(goal).nullable().optional().transform(value => value ?? []), nodes: z.array(node).nullable().optional().transform(value => value ?? []) });

export function createLayoutRepository({ mutate }: RoadmapTransactions) {
  return {
    update(actor: SpaceActor, spaceId: string, roadmapId: string, raw: unknown) {
      const parsed = layoutSchema.safeParse(raw);
      if (!parsed.success) throw new SpaceError("invalid_request"); const input = parsed.data;
      if (input.milestones.length + input.goals.length + input.nodes.length > 1000) throw new SpaceError("invalid_request");
      return mutate(actor, spaceId, roadmapId, input.expected_version, async (tx, version) => {
        for (const item of input.milestones) {
          if (!(await tx.query("UPDATE space_roadmap_milestones SET position_x=$1,position_y=$2,width=$3,height=$4,version=version+1,updated_at=now() WHERE id=$5 AND roadmap_id=$6 AND space_id=$7 AND archived_at IS NULL",
            [item.position_x, item.position_y, item.width, item.height, item.id, roadmapId, spaceId])).rowCount) throw new SpaceError("not_found");
        }
        for (const item of input.goals) {
          if (!(await tx.query(`UPDATE space_roadmap_goals g SET milestone_id=m.id,position_x=$1,position_y=$2,version=g.version+1,updated_at=now() FROM space_roadmap_milestones m
            WHERE g.id=$3 AND g.roadmap_id=$4 AND g.space_id=$5 AND g.archived_at IS NULL AND m.id=$6 AND m.roadmap_id=g.roadmap_id AND m.space_id=g.space_id AND m.archived_at IS NULL`,
          [item.position_x, item.position_y, item.id, roadmapId, spaceId, item.milestone_id])).rowCount) throw new SpaceError("not_found");
        }
        for (const item of input.nodes) {
          const result = item.milestone_id
            ? await tx.query(`UPDATE space_roadmap_nodes n SET milestone_id=m.id,position_x=$1,position_y=$2,version=n.version+1,updated_at=now() FROM space_roadmap_milestones m
              WHERE n.id=$3 AND n.roadmap_id=$4 AND n.space_id=$5 AND n.archived_at IS NULL AND m.id=$6 AND m.roadmap_id=n.roadmap_id AND m.space_id=n.space_id AND m.archived_at IS NULL`,
            [item.position_x, item.position_y, item.id, roadmapId, spaceId, item.milestone_id])
            : await tx.query("UPDATE space_roadmap_nodes SET milestone_id=NULL,position_x=$1,position_y=$2,version=version+1,updated_at=now() WHERE id=$3 AND roadmap_id=$4 AND space_id=$5 AND archived_at IS NULL", [item.position_x, item.position_y, item.id, roadmapId, spaceId]);
          if (!result.rowCount) throw new SpaceError("not_found");
        }
        await roadmapChildNotification(tx, actor, spaceId, roadmapId, roadmapId, "layout.updated", version); return { graph_version: version };
      });
    },
  };
}
