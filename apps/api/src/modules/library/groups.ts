import { randomUUID } from "node:crypto";
import { z } from "zod";
import { SpaceError, clientInteger, trimSpace } from "../spaces/model.js";
import { itemAudience, itemColumns, itemResponse, type ItemRow } from "./model.js";
import { organizationName, parseOrganization, type OrganizationTransaction } from "./organization-model.js";
const rule = z.object({ field: z.enum(["favorite", "hidden", "tag", "mime", "filename", "album"]), op: z.string(), value: z.unknown() }).refine(value => {
  if (["favorite", "hidden"].includes(value.field)) return value.op === "is" && typeof value.value === "boolean";
  return (value.op === "contains" || value.field === "album" && value.op === "in" || value.field === "mime" && value.op === "prefix") &&
    typeof value.value === "string" && Boolean(trimSpace(value.value)) && Buffer.byteLength(value.value) <= 255;
});
const rules = z.object({ all: z.array(rule).max(12).nullish().transform(value => value ?? null) }).nullish().transform(value => value ?? { all: null });
type GroupRow = Record<string, unknown> & { version: string };
const columns = "id,space_id,name,rules,created_by_user_id,version,created_at,updated_at";
const response = (row: GroupRow) => ({ ...row, version: clientInteger(row.version) });
export function createLibraryGroups(transaction: OrganizationTransaction) {
  return {
    list: (userId: string, spaceId: string) => transaction(userId, spaceId, false, async tx => ({ groups:
      (await tx.query<GroupRow>(`SELECT ${columns} FROM space_library_groups WHERE space_id=$1 ORDER BY lower(name),id`, [spaceId])).rows.map(response) })),
    create(userId: string, spaceId: string, raw: unknown) {
      const value = parseOrganization(z.object({ name: organizationName, rules }), raw);
      return transaction(userId, spaceId, true, async tx => {
        if (Number((await tx.query("SELECT count(*) AS count FROM space_library_groups WHERE space_id=$1", [spaceId])).rows[0].count) >= 100) throw new SpaceError("invalid_request");
        const row = (await tx.query<GroupRow>(`INSERT INTO space_library_groups(id,space_id,name,rules,created_by_user_id) VALUES($1,$2,$3,$4::jsonb,$5) RETURNING ${columns}`, [`group_${randomUUID()}`, spaceId, value.name, JSON.stringify(value.rules), userId])).rows[0]!;
        return response(row);
      });
    },
    items(userId: string, spaceId: string, id: string) {
      return transaction(userId, spaceId, false, async tx => {
        const row = (await tx.query<{ rules: unknown }>("SELECT rules FROM space_library_groups WHERE space_id=$1 AND id=$2", [spaceId, id])).rows[0];
        if (!row) throw new SpaceError("not_found");
        const value = parseOrganization(rules, row.rules), args: unknown[] = [spaceId, userId];
        const conditions = ["i.space_id=$1", "i.lifecycle_state='ready'", "i.hidden=FALSE", itemAudience("i", "$2")];
        for (const entry of value.all ?? []) {
          args.push(entry.value); const parameter = `$${args.length}`;
          switch (entry.field) {
            case "favorite": case "hidden": conditions.push(`i.${entry.field}=${parameter}::boolean`); break;
            case "tag": conditions.push(`i.tags ? ${parameter}::text`); break;
            case "mime": conditions.push(`f.intrinsic_metadata->>'server_detected_mime_type' LIKE ${parameter}::text||'%'`); break;
            case "filename": conditions.push(`i.display_name ILIKE '%'||${parameter}::text||'%'`); break;
            case "album": conditions.push(`EXISTS(SELECT 1 FROM space_album_items ai JOIN space_albums a ON a.id=ai.album_id WHERE ai.space_library_item_id=i.id AND a.id=${parameter}::text AND a.space_id=i.space_id)`); break;
          }
        }
        return { items: (await tx.query<ItemRow>(`SELECT ${itemColumns} FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE ${conditions.join(" AND ")} ORDER BY i.added_at DESC,i.id DESC LIMIT 200`, args)).rows.map(itemResponse) };
      });
    },
  };
}
