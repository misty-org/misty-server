import type { PoolClient } from "pg";
import { requireLiveAppSession, type AppSession } from "../app-runtime/repository.js";
import { SpaceError } from "./model.js";
export type SpaceActor = { userId: string; appSession?: AppSession };
export async function requireActiveAccount(tx: PoolClient, userId: string) {
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [userId])).rowCount) throw new SpaceError("not_authenticated");
}
export async function requireSpaceActor(tx: PoolClient, actor: SpaceActor, spaceId: string, owner = false, appScope = "spaces.read") {
  if (!(await tx.query(`SELECT id FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR ${owner ? "UPDATE" : "SHARE"}`, [spaceId])).rowCount) throw new SpaceError("forbidden");
  await requireActiveAccount(tx, actor.userId);
  if (actor.appSession) {
    if (owner || actor.appSession.space_id !== spaceId || actor.appSession.user_id !== actor.userId) throw new SpaceError("forbidden");
    await requireLiveAppSession(tx, actor.appSession, appScope);
  }
  const member = (await tx.query<{ role: string }>("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE", [spaceId, actor.userId])).rows[0];
  if (!member || owner && member.role !== "owner") throw new SpaceError("forbidden");
  return member;
}
