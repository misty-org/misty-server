import type { PoolClient } from "pg";
import { requireActiveAccount, requireSpaceActor, type SpaceActor } from "../spaces/access.js";

/** Account-owned provider data still requires the current mounted App's Space. */
export async function requireConnectionActor(tx: PoolClient, actor: SpaceActor, scope: string) {
  if (actor.appSession) await requireSpaceActor(tx, actor, actor.appSession.space_id, false, scope);
  else await requireActiveAccount(tx, actor.userId);
}
