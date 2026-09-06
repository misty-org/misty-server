import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { lockStorage } from "./quota.js";
import type { ObjectStore } from "./object-store.js";

export async function queueObjectDeletion(tx: PoolClient, key: string, expires: Date) {
  // A still-valid PUT URL could recreate an object after an immediate deletion.
  await tx.query(`INSERT INTO object_deletion_jobs(object_key,not_before) VALUES($1,$2::timestamptz+interval '1 minute')
    ON CONFLICT(object_key) DO UPDATE SET not_before=GREATEST(object_deletion_jobs.not_before,EXCLUDED.not_before)`, [key, expires]);
}
export function createStorageJobs(pool: Pool, store: Pick<ObjectStore, "delete">) {
  const tx = <T>(run: (client: PoolClient) => Promise<T>) => withTransaction(pool, run, { mode: "service" });
  return {
    async runOnce() {
      let work = 0;
      const expired = await tx(async (client) => (await client.query<{ id: string; space_id: string; user_id: string }>(`SELECT id,space_id,user_id FROM space_library_uploads
        WHERE purpose IN ('note_attachment','drawing_attachment') AND expires_at<=now() AND state IN ('initiated','uploaded_unverified') ORDER BY expires_at,id LIMIT 25`)).rows);
      for (const candidate of expired) {
        work += await tx(async (client) => {
          // Same order as requests, without requiring an active account or Space:
          // cleanup must still release reservations belonging to retired owners.
          if (!(await client.query("SELECT id FROM spaces WHERE id=$1 FOR SHARE", [candidate.space_id])).rowCount) return 0;
          await client.query("SELECT id FROM users WHERE id=$1 FOR SHARE", [candidate.user_id]);
          await lockStorage(client, candidate.user_id, candidate.space_id);
          const upload = (await client.query<{ object_key: string; expires_at: Date; requested_byte_size: string }>(`SELECT object_key,expires_at,requested_byte_size FROM space_library_uploads
            WHERE id=$1 AND expires_at<=now() AND state IN ('initiated','uploaded_unverified') FOR UPDATE`, [candidate.id])).rows[0];
          if (!upload) return 0;
          const reservation = (await client.query<{ reserved_bytes: string }>(`UPDATE space_upload_reservations SET state='released',updated_at=now()
            WHERE upload_id=$1 AND state='active' RETURNING reserved_bytes`, [candidate.id])).rows[0];
          if (reservation) {
            const changed = await client.query(`UPDATE space_storage_usage SET reserved_bytes=reserved_bytes-$1,version=version+1,updated_at=now()
              WHERE space_id=$2 AND reserved_bytes >= $1`, [reservation.reserved_bytes, candidate.space_id]);
            if (!changed.rowCount) throw new Error("Storage reservation accounting mismatch");
          }
          await client.query("UPDATE space_library_uploads SET state='expired',error_code='upload_expired',version=version+1,updated_at=now() WHERE id=$1", [candidate.id]);
          await queueObjectDeletion(client, upload.object_key, upload.expires_at);
          await client.query(`INSERT INTO space_library_audit_events(request_id,security_domain_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details)
            SELECT $1,security_domain_id,space_id,user_id,'library.upload.expired','upload',id,'failed',$3::jsonb FROM space_library_uploads WHERE id=$2`,
          [`req_${randomUUID()}`, candidate.id, JSON.stringify({ released_bytes: Number(reservation?.reserved_bytes ?? 0) })]);
          return 1;
        });
      }
      const leaseId = randomUUID();
      const jobs = await tx(async (client) => (await client.query<{ object_key: string }>(`WITH candidates AS (
        SELECT object_key FROM object_deletion_jobs WHERE not_before<=now() AND (lease_expires_at IS NULL OR lease_expires_at<=now())
        ORDER BY not_before,object_key FOR UPDATE SKIP LOCKED LIMIT 10)
        UPDATE object_deletion_jobs j SET lease_id=$1,lease_expires_at=now()+interval '2 minutes',attempts=attempts+1
        FROM candidates c WHERE j.object_key=c.object_key RETURNING j.object_key`, [leaseId])).rows);
      for (const job of jobs) {
        const eligible = await tx(async (client) => {
          const row = await client.query(`SELECT 1 FROM object_deletion_jobs j WHERE object_key=$1 AND lease_id=$2 AND not_before<=now()
            AND NOT EXISTS(SELECT 1 FROM users u WHERE u.avatar_version>0 AND COALESCE(u.avatar_object_key,'avatars/'||u.id)=j.object_key)
            AND NOT EXISTS(SELECT 1 FROM ai_conversation_attachments a WHERE a.lifecycle_state<>'deleted'
              AND (a.object_key=j.object_key OR a.model_object_key=j.object_key))
            AND NOT EXISTS(SELECT 1 FROM library_blobs b WHERE b.r2_object_key=j.object_key AND b.lifecycle_state<>'deleted')
            AND NOT EXISTS(SELECT 1 FROM library_blobs b JOIN library_legal_holds h ON h.active AND h.target_kind='blob' AND h.target_id=b.id WHERE b.r2_object_key=j.object_key)
            AND NOT EXISTS(SELECT 1 FROM library_blobs b JOIN library_files f ON f.blob_id=b.id JOIN library_legal_holds h
              ON h.active AND h.target_kind='file' AND h.target_id=f.id WHERE b.r2_object_key=j.object_key)
            AND NOT EXISTS(SELECT 1 FROM library_blobs b JOIN library_files f ON f.blob_id=b.id JOIN space_note_assets a ON a.file_id=f.id
              JOIN library_legal_holds h ON h.active AND ((h.target_kind='note_asset' AND h.target_id=a.id) OR (h.target_kind='note' AND h.target_id=a.note_id)) WHERE b.r2_object_key=j.object_key)
            AND NOT EXISTS(SELECT 1 FROM library_blobs b JOIN library_files f ON f.blob_id=b.id JOIN space_drawing_assets a ON a.file_id=f.id
              JOIN library_legal_holds h ON h.active AND ((h.target_kind='drawing_asset' AND h.target_id=a.id) OR (h.target_kind='drawing' AND h.target_id=a.drawing_id)) WHERE b.r2_object_key=j.object_key)
            AND NOT EXISTS(SELECT 1 FROM space_library_uploads u WHERE u.object_key=j.object_key AND u.expires_at>now())`, [job.object_key, leaseId]);
          return row.rowCount === 1;
        });
        let deleted = false;
        if (eligible) {
          try { await store.delete(job.object_key); deleted = true; } catch { /* Preserve the durable job for a bounded retry. */ }
        }
        await tx(async (client) => {
          if (deleted) await client.query("DELETE FROM object_deletion_jobs WHERE object_key=$1 AND lease_id=$2", [job.object_key, leaseId]);
          else await client.query(`UPDATE object_deletion_jobs SET lease_id=NULL,lease_expires_at=NULL,
            not_before=now()+LEAST(3600,30*power(2,LEAST(attempts,7))) * interval '1 second' WHERE object_key=$1 AND lease_id=$2`, [job.object_key, leaseId]);
        });
        work++;
      }
      return work;
    },
  };
}
