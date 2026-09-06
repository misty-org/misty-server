import { z } from "zod";
import { SpaceError, trimSpace } from "../spaces/model.js";
import { itemAudience, itemColumns, itemResponse, type ItemRow } from "./model.js";
import { libraryAudit } from "./mutation-model.js";
import { organizationVersion, parseOrganization, lockAlbum, requireVisibleItems, readAlbum, type OrganizationTransaction } from "./organization-model.js";
const inputIds = z.array(z.string()).nullish().transform(value => value ?? []);
export function createAlbumItems(transaction: OrganizationTransaction) {
  return {
    list: (userId: string, spaceId: string, albumId: string) => transaction(userId, spaceId, false, async tx => ({ items:
      (await tx.query<ItemRow>(`SELECT ${itemColumns} FROM space_library_items i JOIN library_files f ON f.id=i.file_id
        JOIN space_album_items ai ON ai.space_library_item_id=i.id JOIN space_albums a ON a.id=ai.album_id
        WHERE a.id=$1 AND a.space_id=$2 AND i.space_id=$2 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND ${itemAudience("i", "$3")}
        ORDER BY CASE a.sort_mode WHEN 'oldest' THEN extract(epoch FROM COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at))
        WHEN 'newest' THEN -extract(epoch FROM COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) ELSE ai.position::double precision END,ai.added_at DESC LIMIT 200`, [albumId, spaceId, userId])).rows.map(itemResponse) })),
    add(userId: string, spaceId: string, albumId: string, raw: unknown) {
      const value = parseOrganization(z.object({ item_ids: inputIds }), raw), ids = [...new Set(value.item_ids.map(trimSpace).filter(Boolean))];
      if (!ids.length || ids.length > 200) throw new SpaceError("invalid_request");
      return transaction(userId, spaceId, true, async tx => {
        await requireVisibleItems(tx, userId, spaceId, ids); await lockAlbum(tx, spaceId, albumId);
        await tx.query("INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id) SELECT $1,unnest($2::text[]),$3 ON CONFLICT DO NOTHING", [albumId, ids, userId]);
        await tx.query("UPDATE space_albums SET version=version+1,updated_at=now() WHERE id=$1", [albumId]);
      });
    },
    remove(userId: string, spaceId: string, albumId: string, itemId: string) {
      return transaction(userId, spaceId, true, async tx => {
        await lockAlbum(tx, spaceId, albumId);
        if (!(await tx.query(`DELETE FROM space_album_items ai USING space_library_items i WHERE ai.album_id=$1 AND ai.space_library_item_id=$2 AND i.id=ai.space_library_item_id AND i.space_id=$3 AND ${itemAudience("i", "$4")}`, [albumId, itemId, spaceId, userId])).rowCount) throw new SpaceError("not_found");
        await tx.query("UPDATE space_albums SET version=version+1,updated_at=now() WHERE id=$1", [albumId]);
      });
    },
    reorder(userId: string, spaceId: string, albumId: string, raw: unknown) {
      const value = parseOrganization(z.object({ version: organizationVersion, item_ids: inputIds }), raw), ids = value.item_ids;
      if (!ids.length || ids.length > 1000 || new Set(ids.map(trimSpace).filter(Boolean)).size !== ids.length) throw new SpaceError("invalid_request");
      return transaction(userId, spaceId, true, async tx => {
        await requireVisibleItems(tx, userId, spaceId, ids); await lockAlbum(tx, spaceId, albumId, value.version);
        if ((await tx.query(`UPDATE space_album_items ai SET position=ordered.position-1 FROM unnest($2::text[]) WITH ORDINALITY ordered(id,position)
          WHERE ai.album_id=$1 AND ai.space_library_item_id=ordered.id`, [albumId, ids])).rowCount !== ids.length) throw new SpaceError("invalid_request");
        await tx.query("UPDATE space_albums SET version=version+1,updated_at=now() WHERE id=$1", [albumId]);
        await libraryAudit(tx, userId, spaceId, "library.album.reordered", "album", albumId, { count: ids.length });
        return readAlbum(tx, userId, spaceId, albumId);
      });
    },
  };
}
