import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { SpaceError, clientInteger } from "../spaces/model.js";
import { itemAudience, requireLibraryPermission } from "./model.js";
import { requireLibraryGrant } from "./reauthentication.js";

export function createLibraryDownloadRepository(pool: Pool) {
  return {
    resolve(userId: string, spaceId: string, itemId: string, original: boolean, token: string) {
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL statement_timeout='5s'"); await tx.query("SET LOCAL lock_timeout='2s'");
        await requireLibraryPermission(tx, userId, spaceId);
        await requireLibraryPermission(tx, userId, spaceId, "library.download");
        const row = (await tx.query<{ object_key: string; filename: string; mime_type: string; byte_size: string; sha256: string; rendition: boolean; hidden: boolean; lifecycle_state: string; ready: boolean }>(`
          SELECT COALESCE(rb.r2_object_key,b.r2_object_key) AS object_key,CASE WHEN $4 THEN f.original_filename ELSE i.display_name END AS filename,
            COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type) AS mime_type,COALESCE(rb.byte_size,b.byte_size) AS byte_size,
            COALESCE(rb.sha256,b.sha256) AS sha256,rb.id IS NOT NULL AS rendition,i.hidden,i.lifecycle_state,
            i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready' AS ready
          FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
          LEFT JOIN library_item_versions v ON NOT $4 AND v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready'
          LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'
          WHERE i.id=$1 AND i.space_id=$2 AND ${itemAudience("i", "$3")}`, [itemId, spaceId, userId, original])).rows[0];
        if (!row) throw new SpaceError("not_found");
        await requireLibraryGrant(tx, userId, spaceId, row.lifecycle_state === "trash" ? "recently_deleted" : row.hidden ? "hidden" : "", token);
        if (!row.ready) throw new SpaceError("not_found");
        await tx.query(`INSERT INTO space_library_item_views(space_id,space_library_item_id,user_id) VALUES($1,$2,$3)
          ON CONFLICT(space_id,space_library_item_id,user_id) DO UPDATE SET view_count=space_library_item_views.view_count+1,last_viewed_at=now()`, [spaceId, itemId, userId]);
        return { objectKey: row.object_key, filename: row.filename, mimeType: row.mime_type, byteSize: clientInteger(row.byte_size), sha256: row.sha256, rendition: row.rendition };
      }, { mode: "service" });
    },
  };
}
