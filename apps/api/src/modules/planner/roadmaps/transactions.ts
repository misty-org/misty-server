import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../../spaces/access.js";
import { clientInteger, SpaceError } from "../../spaces/model.js";
import { requirePlannerActor } from "../access.js";
import { roadmapAudience, roadmapColumns, type RoadmapRow } from "./model.js";

export async function roadmapNotification(tx: PoolClient, actor: SpaceActor, spaceId: string, id: string, type: string, version?: number) {
  const payload = { roadmap_id: id, ...(version === undefined ? {} : { graph_version: version }) };
  const event = (await tx.query<{ id: string }>(`INSERT INTO space_events(space_id,actor_user_id,event_type,entity_id,payload)
    VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id`, [spaceId, actor.userId, `roadmap.${type}`, id, JSON.stringify(payload)])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
}
export async function lockRoadmap(tx: PoolClient, actor: SpaceActor, spaceId: string, id: string, expected: number | string) {
  const row = (await tx.query<RoadmapRow & { graph_version: string }>(`SELECT ${roadmapColumns} FROM space_roadmaps r
    WHERE r.id=$1 AND r.space_id=$2 AND r.archived_at IS NULL AND ${roadmapAudience("$3")} FOR UPDATE OF r`, [id, spaceId, actor.userId])).rows[0];
  if (!row) throw new SpaceError("not_found");
  if (BigInt(row.graph_version) !== BigInt(expected)) throw new SpaceError("version_conflict");
  return row;
}
export function createRoadmapTransactions(pool: Pool) {
  const transaction = <T>(actor: SpaceActor, spaceId: string, write: boolean, operation: (tx: PoolClient) => Promise<T>) => withTransaction(pool, async tx => {
    await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
    await requirePlannerActor(tx, actor, spaceId, write ? "roadmaps.write" : "roadmaps.read", write);
    return operation(tx);
  }, { mode: "service" }, write ? undefined : { isolationLevel: "repeatable read" });
  const mutate = <T>(actor: SpaceActor, spaceId: string, roadmapId: string, expected: number | string, operation: (tx: PoolClient, version: number) => Promise<T>) =>
    transaction(actor, spaceId, true, async tx => {
      await lockRoadmap(tx, actor, spaceId, roadmapId, expected);
      const row = (await tx.query<{ graph_version: string }>("UPDATE space_roadmaps SET graph_version=graph_version+1,updated_at=now() WHERE id=$1 AND space_id=$2 RETURNING graph_version", [roadmapId, spaceId])).rows[0]!;
      return operation(tx, clientInteger(row.graph_version));
    });
  return { transaction, mutate };
}
export type RoadmapTransactions = ReturnType<typeof createRoadmapTransactions>;
export async function roadmapChildNotification(tx: PoolClient, actor: SpaceActor, spaceId: string, roadmapId: string, entityId: string, kind: string, version: number, extra: Record<string, unknown> = {}) {
  const event = (await tx.query<{ id: string }>("INSERT INTO space_events(space_id,actor_user_id,event_type,entity_id,payload) VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id",
    [spaceId, actor.userId, `roadmap.${kind}`, entityId, JSON.stringify({ ...extra, roadmap_id: roadmapId, graph_version: version })])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
}
