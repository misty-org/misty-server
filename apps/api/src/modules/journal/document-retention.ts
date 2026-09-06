import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";

export function createDocumentRetention(pool: Pool) {
  return {
    async runOnce() {
      let count = 0;
      for (const kind of ["note", "drawing"] as const) {
        const table = kind === "note" ? "space_notes" : "space_drawings";
        const eligible = `d.lifecycle_state='deleting' AND EXISTS(SELECT 1 FROM space_${kind}_control_outbox o
            WHERE o.${kind}_id=d.id AND o.command='purge' AND o.delivered_at IS NOT NULL)
          AND NOT EXISTS(SELECT 1 FROM space_${kind}_assets a WHERE a.${kind}_id=d.id AND a.lifecycle_state<>'deleted')
          AND NOT EXISTS(SELECT 1 FROM space_library_uploads u JOIN space_upload_reservations r ON r.upload_id=u.id
            WHERE u.${kind}_id=d.id AND r.state='active')
          AND NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind='${kind}' AND h.target_id=d.id)`;
        const candidates = await withTransaction(pool, async (tx) => (await tx.query<{ id: string; space_id: string }>(`SELECT d.id,d.space_id FROM ${table} d
          WHERE ${eligible} ORDER BY d.updated_at,d.id LIMIT 25`)).rows, { mode: "service" });
        // Room confirmation and completed asset cleanup are both mandatory.
        // Keep metadata if an abandoned upload still owns a storage reservation.
        for (const candidate of candidates) count += await withTransaction(pool, async (tx) => {
          await tx.query("SELECT id FROM spaces WHERE id=$1 FOR SHARE", [candidate.space_id]);
          // Lock before checking children in a separate statement: a reservation
          // that was in flight must become visible before the absence check.
          await tx.query(`SELECT id FROM ${table} WHERE id=$1 FOR UPDATE`, [candidate.id]);
          const deleted = await tx.query(`DELETE FROM ${table} d WHERE d.id=$1 AND ${eligible}`, [candidate.id]);
          return deleted.rowCount ?? 0;
        }, { mode: "service" });
      }
      return count > 0;
    },
  };
}
