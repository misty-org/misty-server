import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { lockStorage } from "../storage/quota.js";
import { queueObjectDeletion } from "../storage/jobs.js";

export function createAssetRetention(pool: Pool) {
  return {
    async runOnce() {
      let count = 0;
      for (const kind of ["note", "drawing"] as const) {
        const table = `space_${kind}_assets`, documents = kind === "note" ? "space_notes" : "space_drawings", parent = `${kind}_id`;
        // Older server versions retired drawings without marking their assets.
        // Normalize that backlog under the same parent lock as new uploads.
        const retired = await withTransaction(pool, async (tx) => (await tx.query<{ id: string; space_id: string }>(`SELECT d.id,d.space_id FROM ${documents} d
          WHERE d.lifecycle_state='deleting' AND EXISTS(SELECT 1 FROM ${table} a WHERE a.${parent}=d.id AND a.lifecycle_state='ready')
          ORDER BY d.updated_at,d.id LIMIT 25`)).rows, { mode: "service" });
        for (const document of retired) await withTransaction(pool, async (tx) => {
          await tx.query("SELECT id FROM spaces WHERE id=$1 FOR SHARE", [document.space_id]);
          const row = (await tx.query<{ updated_at: Date }>(`SELECT updated_at FROM ${documents} WHERE id=$1 AND lifecycle_state='deleting' FOR UPDATE`, [document.id])).rows[0];
          if (row) await tx.query(`UPDATE ${table} SET lifecycle_state='deleting',deleted_at=COALESCE(deleted_at,$2) WHERE ${parent}=$1 AND lifecycle_state='ready'`, [document.id, row.updated_at]);
        }, { mode: "service" });
        const candidates = await withTransaction(pool, async (tx) => (await tx.query<{ id: string; parent_id: string; space_id: string; uploader_user_id: string }>(`SELECT a.id,a.${parent} AS parent_id,d.space_id,a.uploader_user_id
          FROM ${table} a JOIN ${documents} d ON d.id=a.${parent} JOIN library_files f ON f.id=a.file_id
          WHERE a.lifecycle_state IN ('unreferenced','deleting') AND a.deleted_at<=now()-interval '24 hours'
          AND NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND
            ((h.target_kind='${kind}' AND h.target_id=d.id) OR (h.target_kind='${kind}_asset' AND h.target_id=a.id) OR (h.target_kind='file' AND h.target_id=f.id) OR (h.target_kind='blob' AND h.target_id=f.blob_id)))
          ORDER BY a.deleted_at,a.id LIMIT 25`)).rows, { mode: "service" });
        for (const candidate of candidates) {
          count += await withTransaction(pool, async (tx) => {
            if (!(await tx.query("SELECT id FROM spaces WHERE id=$1 FOR SHARE", [candidate.space_id])).rowCount) return 0;
            await tx.query("SELECT id FROM users WHERE id=$1 FOR SHARE", [candidate.uploader_user_id]);
            if (!(await tx.query(`SELECT id FROM ${documents} WHERE id=$1 AND space_id=$2 FOR SHARE`, [candidate.parent_id, candidate.space_id])).rowCount) return 0;
            await lockStorage(tx, candidate.uploader_user_id, candidate.space_id);
            const asset = (await tx.query<{ file_id: string }>(`SELECT file_id FROM ${table} WHERE id=$1 AND ${parent}=$2
              AND lifecycle_state IN ('unreferenced','deleting') AND deleted_at<=now()-interval '24 hours' FOR UPDATE`, [candidate.id, candidate.parent_id])).rows[0];
            if (!asset) return 0;
            const initial = (await tx.query<{ id: string; security_domain_id: string; sha256: string; byte_size: string }>(
              "SELECT b.id,b.security_domain_id,b.sha256,b.byte_size FROM library_files f JOIN library_blobs b ON b.id=f.blob_id WHERE f.id=$1", [asset.file_id])).rows[0];
            if (!initial) throw new Error("Journal asset file is missing");
            await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [`library:blob:${initial.security_domain_id}:${initial.sha256}${initial.byte_size}`]);
            await tx.query("SELECT id FROM library_files WHERE id=$1 FOR UPDATE", [asset.file_id]);
            const blob = (await tx.query<{ r2_object_key: string; lifecycle_state: string }>("SELECT r2_object_key,lifecycle_state FROM library_blobs WHERE id=$1 FOR UPDATE", [initial.id])).rows[0]!;
            const holds = await tx.query(`SELECT 1 FROM library_legal_holds WHERE active AND
              ((target_kind=$5 AND target_id=$6) OR (target_kind=$1 AND target_id=$2) OR (target_kind='file' AND target_id=$3) OR (target_kind='blob' AND target_id=$4)) LIMIT 1`,
            [`${kind}_asset`, candidate.id, asset.file_id, initial.id, kind, candidate.parent_id]);
            if (holds.rowCount) return 0;
            const contributions = (await tx.query<{ logical_bytes: string }>(`UPDATE space_storage_contributions SET state='released',released_at=COALESCE(released_at,now()),updated_at=now()
              WHERE space_id=$1 AND source_kind=$2 AND source_id=$3 AND state IN ('active','recovery') RETURNING logical_bytes`, [candidate.space_id, `${kind}_asset`, candidate.id])).rows;
            const released = contributions.reduce((sum, row) => sum + BigInt(row.logical_bytes), 0n);
            if (released > 0n) {
              const updated = await tx.query(`UPDATE space_storage_usage SET used_bytes=used_bytes-$1,version=version+1,updated_at=now()
                WHERE space_id=$2 AND used_bytes >= $1`, [released, candidate.space_id]);
              if (!updated.rowCount) throw new Error("Journal asset quota accounting mismatch");
            }
            await tx.query(`UPDATE ${table} SET lifecycle_state='deleted',deleted_at=now() WHERE id=$1`, [candidate.id]);
            await tx.query(`UPDATE library_files f SET lifecycle_state='deleted',deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND lifecycle_state IN ('ready','purging')
              AND NOT EXISTS(SELECT 1 FROM space_library_items i WHERE i.file_id=f.id AND i.lifecycle_state<>'deleted')
              AND NOT EXISTS(SELECT 1 FROM space_message_attachments a WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted')
              AND NOT EXISTS(SELECT 1 FROM space_note_assets a WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted')
              AND NOT EXISTS(SELECT 1 FROM space_drawing_assets a WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted')`, [asset.file_id]);
            const deletedBlob = await tx.query(`UPDATE library_blobs b SET lifecycle_state='deleted',deleted_at=now(),version=version+1,updated_at=now()
              WHERE id=$1 AND lifecycle_state IN ('ready','purging')
              AND NOT EXISTS(SELECT 1 FROM library_files f WHERE f.blob_id=b.id AND f.lifecycle_state<>'deleted')
              AND NOT EXISTS(SELECT 1 FROM library_item_versions v WHERE v.rendition_blob_id=b.id AND v.lifecycle_state<>'deleted')
              AND NOT EXISTS(SELECT 1 FROM library_derivatives d WHERE d.derivative_blob_id=b.id AND d.lifecycle_state<>'deleted')
              AND NOT EXISTS(SELECT 1 FROM library_exports e WHERE e.export_blob_id=b.id AND e.state<>'deleted')`, [initial.id]);
            if (deletedBlob.rowCount) {
              const expiry = (await tx.query<{ deadline: Date }>("SELECT GREATEST(now(),COALESCE(max(expires_at),now())) AS deadline FROM space_library_uploads WHERE object_key=$1", [blob.r2_object_key])).rows[0]!.deadline;
              await queueObjectDeletion(tx, blob.r2_object_key, expiry);
            }
            return 1;
          }, { mode: "service" });
        }
      }
      return count > 0;
    },
  };
}
