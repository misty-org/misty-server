import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { requireSpaceActor, type SpaceActor } from "./access.js";
import { SpaceError, clientInteger } from "./model.js";
import { configurablePermissions, spacePermissions } from "./permissions.js";
import { readSpaceAgents } from "../agents/space-memberships.js";
export type PermissionChange = { permission: string; effect: string };
export async function readSpaceMembers(tx: PoolClient, actor: SpaceActor, spaceId: string) {
  await requireSpaceActor(tx, actor, spaceId);
  const members = (await tx.query("SELECT m.space_id,m.user_id,u.name,u.email,m.role,m.joined_at,m.read_message_seq FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=$1 ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,u.name,m.user_id", [spaceId])).rows;
  return { members: members.map((member) => ({ ...member, read_message_seq: clientInteger(member.read_message_seq) })), agents: await readSpaceAgents(tx, actor.userId, spaceId) };
}
export async function memberPermissions(tx: PoolClient, actor: SpaceActor, spaceId: string, memberId: string, change?: PermissionChange) {
  if (actor.appSession) throw new SpaceError("forbidden");
  if (change && (!(configurablePermissions as readonly string[]).includes(change.permission) || !["allow", "deny", "inherit"].includes(change.effect))) throw new SpaceError("invalid_request");
  const actorMember = await requireSpaceActor(tx, actor, spaceId, !!change);
  if (actor.userId !== memberId && actorMember.role !== "owner") throw new SpaceError("forbidden");
  const member = (await tx.query<{ role: string }>(`SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR ${change ? "UPDATE" : "SHARE"}`, [spaceId, memberId])).rows[0];
  if (!member) throw new SpaceError("not_found");
  if (change) {
    if (member.role === "owner") throw new SpaceError("invalid_request");
    if (change.effect === "inherit") await tx.query("DELETE FROM space_member_permission_overrides WHERE space_id=$1 AND user_id=$2 AND permission=$3", [spaceId, memberId, change.permission]);
    else await tx.query(`INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,$3,$4,$5)
      ON CONFLICT(space_id,user_id,permission) DO UPDATE SET effect=EXCLUDED.effect,updated_by_user_id=EXCLUDED.updated_by_user_id,
        version=space_member_permission_overrides.version+1,updated_at=now()`, [spaceId, memberId, change.permission, change.effect, actor.userId]);
    await tx.query(`INSERT INTO space_library_audit_events(request_id,security_domain_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details)
      SELECT $1,security_domain_id,id,$3,'space.permission.updated','member',$4,'success',$5::jsonb FROM spaces WHERE id=$2`,
      [`req_${randomUUID()}`, spaceId, actor.userId, memberId, JSON.stringify(change)]);
  }
  const permissions = await spacePermissions(tx, memberId, { id: spaceId, role: member.role, is_default: false });
  return { permissions: Object.fromEntries(configurablePermissions.map((key) => [key, permissions[key as keyof typeof permissions]])) };
}
