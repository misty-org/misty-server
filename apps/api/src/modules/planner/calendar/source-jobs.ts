import { createHash, timingSafeEqual } from "node:crypto";
import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { sourceColumns, type CalendarSource } from "./source-model.js";
import type { createCalendarSourceService } from "./source-service.js";

export function createCalendarSourceJobs(pool: Pool, service: ReturnType<typeof createCalendarSourceService>) {
  const stopping = new AbortController();
  return {
    abort() { stopping.abort(); },
    async runOnce() {
      if (stopping.signal.aborted) return false;
      const source = await withTransaction(pool, async tx => (await tx.query<CalendarSource>(`SELECT ${sourceColumns} FROM space_calendar_sources c
        WHERE execution_owner='hono' AND disabled_at IS NULL AND status<>'disabled' AND native_sync_available_at<=clock_timestamp()
          AND (native_sync_lease_until IS NULL OR native_sync_lease_until<=clock_timestamp())
          AND (native_sync_requested>native_sync_completed OR last_reconciled_at IS NULL OR last_reconciled_at<now()-interval '15 minutes'
            OR watch_expires_at IS NULL OR watch_expires_at<now()+interval '24 hours' OR watch_resource_id='')
          AND EXISTS(SELECT 1 FROM users u JOIN space_members m ON m.user_id=u.id JOIN spaces s ON s.id=m.space_id
            WHERE u.id=c.connected_by_user_id AND u.lifecycle_state='active' AND s.id=c.space_id AND s.lifecycle_state='active')
          AND EXISTS(SELECT 1 FROM space_integrations i WHERE i.id=c.integration_id AND i.status='active')
        ORDER BY native_sync_available_at,id LIMIT 1`)).rows[0] ?? null, { mode: "service" });
      if (!source) return false;
      try { await service.reconcile(source, stopping.signal); }
      catch (error) {
        await withTransaction(pool, tx => tx.query(`UPDATE space_calendar_sources SET native_sync_available_at=clock_timestamp()+interval '30 seconds'
          WHERE id=$1 AND execution_owner='hono' AND updated_at=$2::timestamptz AND native_sync_lease_id IS NULL`, [source.id, source.revision]), { mode: "service" });
        throw error;
      }
      return true;
    },
    /** Google may deliver the first sync notification before watch registration returns. */
    async callback(channelId: string, channelToken: string, resourceId: string) {
      if (!channelId || !channelToken || !resourceId || channelId.length > 512 || channelToken.length > 512 || resourceId.length > 2048) return "invalid" as const;
      const supplied = createHash("sha256").update(channelToken).digest();
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const row = (await tx.query<{ id: string; watch_token_hash: string }>(`SELECT c.id,c.watch_token_hash FROM space_calendar_sources c
          WHERE c.watch_channel_id=$1 AND c.execution_owner='hono' AND c.status<>'disabled' AND c.disabled_at IS NULL
            AND c.watch_expires_at>clock_timestamp() AND (c.watch_resource_id=$2 OR c.watch_resource_id='')
            AND EXISTS(SELECT 1 FROM users u JOIN space_members m ON m.user_id=u.id JOIN spaces s ON s.id=m.space_id
              WHERE u.id=c.connected_by_user_id AND u.lifecycle_state='active' AND s.id=c.space_id AND s.lifecycle_state='active')
            AND EXISTS(SELECT 1 FROM space_integrations i WHERE i.id=c.integration_id AND i.status='active') FOR UPDATE OF c`, [channelId, resourceId])).rows[0];
        if (!row || !/^[a-f0-9]{64}$/.test(row.watch_token_hash) || !timingSafeEqual(supplied, Buffer.from(row.watch_token_hash, "hex"))) return "missing" as const;
        // Do not change updated_at: a notification during import must queue the
        // next generation without invalidating the source's in-flight revision.
        await tx.query(`UPDATE space_calendar_sources SET native_sync_requested=native_sync_requested+1,
          native_sync_available_at=LEAST(native_sync_available_at,clock_timestamp()) WHERE id=$1`, [row.id]);
        return "accepted" as const;
      }, { mode: "service" });
    },
  };
}
