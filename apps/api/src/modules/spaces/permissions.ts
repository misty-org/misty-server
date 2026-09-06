import type { PoolClient } from "pg";

export const configurablePermissions = ["messages.read", "messages.write", "library.view", "library.upload", "attachments.upload", "library.add", "library.edit", "library.download",
  "library.import", "storage.view_own_usage", "storage.view_member_usage", "storage.manage", "studio.view", "studio.manage", "agents.run", "agents.manage", "tasks.view", "tasks.manage", "integrations.manage"] as const;
const memberPermissions = new Set<string>(["messages.read", "messages.write", "attachments.upload", "library.view", "library.upload", "library.add", "library.edit", "library.download",
  "library.import", "storage.view_own_usage", "studio.view", "agents.run", "tasks.view", "tasks.manage"]);
export const initialRolePermissions = ["space.view", "messages.read", "messages.write", "attachments.upload", "library.view", "library.upload", "library.add", "library.edit", "library.download", "library.import", "storage.view_own_usage", "tasks.view", "tasks.manage"];
export async function spacePermissions(tx: PoolClient, userId: string, space: { id: string; role: string; is_default: boolean }) {
  const owner = space.role === "owner", permissions: Record<string, boolean> = {};
  for (const permission of configurablePermissions) permissions[permission] = owner || memberPermissions.has(permission);
  if (!owner) for (const row of (await tx.query<{ permission: string; effect: string }>("SELECT permission,effect FROM space_member_permission_overrides WHERE space_id=$1 AND user_id=$2", [space.id, userId])).rows) {
    if (Object.hasOwn(permissions, row.permission)) permissions[row.permission] = row.effect === "allow";
  }
  if (!permissions["messages.read"]) permissions["messages.write"] = false;
  if (!permissions["messages.read"] || !permissions["messages.write"]) permissions["attachments.upload"] = false;
  if (!permissions["tasks.view"]) permissions["tasks.manage"] = false;
  return { ...permissions, "space.invite": owner, "space.rename": owner, "space.transfer": owner && !space.is_default, "space.delete": owner && !space.is_default, "space.leave": !owner };
}
