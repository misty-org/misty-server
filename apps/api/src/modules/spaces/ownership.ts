import type { PoolClient } from "pg";
import { readEntitlements } from "../entitlements/lookup.js";
import { refreshSpaceWallet } from "../usage/space-wallet.js";
import type { SpaceActor } from "./access.js";
import { SpaceError } from "./model.js";
import { spaceEvent } from "./templates.js";

export async function transferOwnership(tx: PoolClient, actor: SpaceActor, spaceId: string, targetId: string) {
  if (actor.appSession) throw new SpaceError("forbidden");
  const space = (await tx.query<{ owner_user_id: string; is_default: boolean; now: Date }>(
    "SELECT owner_user_id,is_default,now() AS now FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR UPDATE", [spaceId])).rows[0];
  if (!space) throw new SpaceError("forbidden");
  // The usage subsystem can reclaim reservations charged to other members.
  // Lock their accounts with both owners in one sorted set before touching wallets.
  const staleUsers = (await tx.query<{ user_id: string }>(`SELECT DISTINCT user_id FROM hosted_ai_reservations
    WHERE space_id=$1 AND status='reserved' AND COALESCE(lease_expires_at,created_at+interval '15 minutes')<=$2`, [spaceId, space.now])).rows.map((row) => row.user_id);
  const ids = [...new Set([actor.userId, targetId, space.owner_user_id, ...staleUsers])].sort();
  const accounts = (await tx.query<{ id: string; lifecycle_state: string }>("SELECT id,lifecycle_state FROM users WHERE id=ANY($1::text[]) ORDER BY id FOR UPDATE", [ids])).rows;
  if (!accounts.some((row) => row.id === actor.userId && row.lifecycle_state === "active")) throw new SpaceError("not_authenticated");
  const owner = (await tx.query<{ role: string }>("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR UPDATE", [spaceId, actor.userId])).rows[0];
  if (space.owner_user_id !== actor.userId || owner?.role !== "owner") throw new SpaceError("forbidden");
  if (space.is_default) throw new SpaceError("default_space_protected");
  if (!targetId || targetId === actor.userId) throw new SpaceError("invalid_request");
  const member = (await tx.query<{ role: string }>("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR UPDATE", [spaceId, targetId])).rows[0];
  if (!member || !accounts.some((row) => row.id === targetId && row.lifecycle_state === "active")) throw new SpaceError("not_found");
  if (member.role !== "member") throw new SpaceError("invalid_request");
  // Creation takes its account lock before this same advisory lock. Keep that
  // order so a new Space and a transfer cannot each consume the final plan slot.
  await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`spaces:owner:${targetId}`]);
  const count = BigInt((await tx.query<{ count: string }>("SELECT count(*) FROM spaces WHERE owner_user_id=$1 AND lifecycle_state<>'deleted'", [targetId])).rows[0]!.count);
  if (count >= BigInt((await readEntitlements(tx, targetId)).maxOwnedSpaces)) throw new SpaceError("space_ownership_limit_reached");
  await refreshSpaceWallet(tx, { spaceId, ownerId: actor.userId, now: space.now });
  const busy = (await tx.query<{ busy: boolean }>(`SELECT
    EXISTS(SELECT 1 FROM space_upload_reservations WHERE space_id=$1 AND state='active') OR
    EXISTS(SELECT 1 FROM space_rendition_reservations WHERE space_id=$1 AND state='active') OR
    EXISTS(SELECT 1 FROM hosted_ai_reservations WHERE space_id=$1 AND status='reserved') AS busy`, [spaceId])).rows[0]!.busy;
  if (busy) throw new SpaceError("version_conflict");
  await tx.query("UPDATE space_members SET role='member' WHERE space_id=$1 AND user_id=$2", [spaceId, actor.userId]);
  await tx.query("UPDATE space_members SET role='owner' WHERE space_id=$1 AND user_id=$2", [spaceId, targetId]);
  await tx.query("UPDATE spaces SET owner_user_id=$2,updated_at=now() WHERE id=$1", [spaceId, targetId]);
  await tx.query("UPDATE security_domains SET owner_user_id=$2,version=version+1,updated_at=now() WHERE space_id=$1 AND kind='space'", [spaceId, targetId]);
  // Preserve consumption through a lower allowance. Existing oversized storage
  // stays accessible; the new owner's capacity applies to subsequent reservations.
  await refreshSpaceWallet(tx, { spaceId, ownerId: targetId, now: space.now, reclaimStale: false });
  await spaceEvent(tx, spaceId, actor.userId, "owner.transferred", targetId, {});
}
