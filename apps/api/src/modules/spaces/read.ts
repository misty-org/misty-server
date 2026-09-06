import type { PoolClient } from "pg";
import { SpaceError, clientInteger } from "./model.js";
import { spacePermissions } from "./permissions.js";
const fields = `s.id,s.security_domain_id,s.owner_user_id,s.name,s.is_default,m.role,
  (SELECT count(*) FROM space_members sm WHERE sm.space_id=s.id) AS member_count,
  (SELECT count(*) FROM space_invitations i WHERE i.space_id=s.id AND i.expires_at>now() AND i.revoked_at IS NULL AND i.consumed_at IS NULL) AS pending_count,
  (EXISTS(SELECT 1 FROM space_members sm WHERE sm.space_id=s.id AND sm.role='member') OR
   EXISTS(SELECT 1 FROM space_invitations i WHERE i.space_id=s.id AND i.expires_at>now() AND i.revoked_at IS NULL AND i.consumed_at IS NULL)) AS is_shared,
  s.created_at,s.updated_at`;
type SpaceRow = { id: string; security_domain_id: string; owner_user_id: string; name: string; is_default: boolean; role: string; member_count: string; pending_count: string; is_shared: boolean; created_at: Date; updated_at: Date };
export async function readSpaces(tx: PoolClient, userId: string, spaceId?: string) {
  const rows = (await tx.query<SpaceRow>(`SELECT ${fields} FROM spaces s JOIN space_members m ON m.space_id=s.id
    WHERE m.user_id=$1 AND s.lifecycle_state='active' ${spaceId ? "AND s.id=$2" : ""} ORDER BY s.updated_at DESC,s.id`, spaceId ? [userId, spaceId] : [userId])).rows;
  const spaces = [];
  for (const row of rows) spaces.push({ ...row, member_count: clientInteger(row.member_count), pending_count: clientInteger(row.pending_count), permissions: await spacePermissions(tx, userId, row) });
  return spaces;
}
export async function readSpace(tx: PoolClient, userId: string, id: string) {
  const space = (await readSpaces(tx, userId, id))[0]; if (!space) throw new SpaceError("not_found"); return space;
}
export async function readSetup(tx: PoolClient, spaceId: string) {
  const result = { selected_providers: [] as string[], completed_providers: [] as string[], pending_providers: [] as string[] };
  for (const row of (await tx.query<{ provider: string; status: string }>("SELECT provider,status FROM space_setup_integrations WHERE space_id=$1 ORDER BY provider", [spaceId])).rows) {
    result.selected_providers.push(row.provider);
    (row.status === "configured" || row.status === "skipped" ? result.completed_providers : result.pending_providers).push(row.provider);
  }
  return result;
}
