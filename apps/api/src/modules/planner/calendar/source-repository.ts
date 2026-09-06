import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import type { createLegacyTokenBroker, LegacyTokenLease } from "../../connections/legacy-token-broker.js";
import { requireCalendarSourceActor, type CalendarSourceAction } from "./source-access.js";
import { sourceColumns, sourceResponse, type CalendarSource } from "./source-model.js";
import { eventFingerprint, eventTimes, type GoogleEvent } from "./google-client.js";
import { claimCalendarWorkflows } from "./source-workflows.js";

type Broker = ReturnType<typeof createLegacyTokenBroker>;
async function notification(tx: PoolClient, spaceId: string, userId: string, kind: string, id: string, payload: unknown) {
  const event = (await tx.query<{ id: string }>("INSERT INTO space_events(space_id,actor_user_id,event_type,entity_id,payload) VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id", [spaceId, userId, kind, id, JSON.stringify(payload)])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
}
export function createCalendarSourceRepository(pool: Pool) {
  const transaction = <T>(actor: SpaceActor, spaceId: string, action: CalendarSourceAction, operation: (tx: PoolClient) => Promise<T>) => withTransaction(pool, async tx => {
    await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
    await requireCalendarSourceActor(tx, actor, spaceId, action); return operation(tx);
  }, { mode: "service" });
  const current = async (tx: PoolClient, source: CalendarSource) => {
    const row = (await tx.query<CalendarSource>(`SELECT ${sourceColumns} FROM space_calendar_sources WHERE id=$1 AND space_id=$2 AND updated_at=$3::timestamptz
      AND execution_owner='hono' AND status<>'disabled' AND disabled_at IS NULL
      AND native_sync_lease_id=$4::uuid AND native_sync_lease_until>clock_timestamp() FOR UPDATE`, [source.id, source.space_id, source.revision, source.native_sync_lease_id])).rows[0];
    if (!row) throw new SpaceError("version_conflict"); return row;
  };
  return {
    transaction,
    async claim(actor: SpaceActor, action: CalendarSourceAction, source: CalendarSource) {
      return transaction(actor, source.space_id, action, async tx => (await tx.query<CalendarSource>(`UPDATE space_calendar_sources
        SET native_sync_lease_id=$4,native_sync_lease_until=clock_timestamp()+interval '3 minutes'
        WHERE id=$1 AND space_id=$2 AND updated_at=$3::timestamptz AND execution_owner='hono' AND status<>'disabled' AND disabled_at IS NULL
          AND (native_sync_lease_until IS NULL OR native_sync_lease_until<=clock_timestamp()) RETURNING ${sourceColumns}`,
      [source.id, source.space_id, source.revision, randomUUID()])).rows[0] ?? null);
    },
    async finish(source: CalendarSource, generation: string, success: boolean) {
      await withTransaction(pool, async tx => {
        await tx.query(`UPDATE space_calendar_sources SET native_sync_completed=CASE WHEN $4 THEN GREATEST(native_sync_completed,$3::bigint) ELSE native_sync_completed END,
          native_sync_available_at=clock_timestamp()+CASE WHEN NOT $4 THEN interval '30 seconds' WHEN native_sync_requested>$3::bigint THEN interval '0 seconds' ELSE interval '15 minutes' END,
          native_sync_lease_id=NULL,native_sync_lease_until=NULL WHERE id=$1 AND execution_owner='hono'
          AND native_sync_lease_id=$2::uuid AND native_sync_lease_until>clock_timestamp()`, [source.id, source.native_sync_lease_id, generation, success]);
      }, { mode: "service" });
    },
    async beginWatch(actor: SpaceActor, action: CalendarSourceAction, source: CalendarSource, broker: Broker, lease: LegacyTokenLease, channel: { id: string; hash: string; expiresAt: Date }) {
      return transaction(actor, source.space_id, action, async tx => {
        await broker.assertCurrent(tx, lease); await current(tx, source);
        return (await tx.query<CalendarSource>(`UPDATE space_calendar_sources SET watch_channel_id=$2,watch_resource_id='',watch_token_hash=$3,watch_expires_at=$4,
          updated_at=clock_timestamp() WHERE id=$1 RETURNING ${sourceColumns}`, [source.id, channel.id, channel.hash, channel.expiresAt])).rows[0]!;
      });
    },
    async finishWatch(actor: SpaceActor, action: CalendarSourceAction, source: CalendarSource, broker: Broker, lease: LegacyTokenLease, resourceId: string, expiresAt: Date) {
      return transaction(actor, source.space_id, action, async tx => {
        await broker.assertCurrent(tx, lease); await current(tx, source);
        return (await tx.query<CalendarSource>(`UPDATE space_calendar_sources SET watch_resource_id=$2,watch_expires_at=$3,updated_at=clock_timestamp()
          WHERE id=$1 RETURNING ${sourceColumns}`, [source.id, resourceId, expiresAt])).rows[0]!;
      });
    },
    list(actor: SpaceActor, spaceId: string, action: CalendarSourceAction = "list") {
      return transaction(actor, spaceId, action, async tx => (await tx.query<CalendarSource>(`SELECT ${sourceColumns} FROM space_calendar_sources WHERE space_id=$1 ORDER BY display_name,id`, [spaceId])).rows);
    },
    create(actor: SpaceActor, spaceId: string, input: { integration_id: string; external_calendar_id: string; display_name: string; timezone: string }, broker: Broker, lease: LegacyTokenLease) {
      return transaction(actor, spaceId, "manage", async tx => {
        await broker.assertCurrent(tx, lease);
        const integration = (await tx.query<{ connected_by_user_id: string }>("SELECT connected_by_user_id FROM space_integrations WHERE id=$1 AND space_id=$2 AND provider='google' AND status='active' FOR SHARE", [input.integration_id, spaceId])).rows[0];
        if (!integration) throw new SpaceError("invalid_request");
        const source = (await tx.query<CalendarSource>(`INSERT INTO space_calendar_sources(id,space_id,integration_id,connected_by_user_id,provider,external_calendar_id,display_name,timezone,execution_owner)
          VALUES($1,$2,$3,$4,'google',$5,$6,$7,'hono') ON CONFLICT(space_id,integration_id,external_calendar_id)
          DO UPDATE SET display_name=EXCLUDED.display_name,timezone=EXCLUDED.timezone,status='pending',disabled_at=NULL,native_sync_available_at=now(),native_sync_lease_id=NULL,native_sync_lease_until=NULL,updated_at=clock_timestamp() WHERE space_calendar_sources.execution_owner='hono' RETURNING ${sourceColumns}`,
        [`calendar_source_${randomUUID()}`, spaceId, input.integration_id, integration.connected_by_user_id, input.external_calendar_id, input.display_name, input.timezone])).rows[0]!;
        if (!source) throw new SpaceError("version_conflict");
        await notification(tx, spaceId, actor.userId, "calendar.source_published", source.id, { source: sourceResponse(source) });
        await tx.query(`UPDATE space_setup_integrations SET status='configured',updated_at=now() WHERE space_id=$1 AND provider='google'
          AND EXISTS(SELECT 1 FROM spaces WHERE id=$1 AND owner_user_id=$2)`, [spaceId, actor.userId]);
        return source;
      });
    },
    disable(actor: SpaceActor, spaceId: string, id: string) {
      return transaction(actor, spaceId, "manage", async tx => {
        if (!(await tx.query(`UPDATE space_calendar_sources SET status='disabled',disabled_at=now(),sync_token='',watch_channel_id='',watch_resource_id='',watch_token_hash='',watch_expires_at=NULL,native_sync_lease_id=NULL,native_sync_lease_until=NULL,updated_at=clock_timestamp()
          WHERE id=$1 AND space_id=$2`, [id, spaceId])).rowCount) throw new SpaceError("not_found");
        await notification(tx, spaceId, actor.userId, "calendar.source_disabled", id, {});
      });
    },
    applyPage(actor: SpaceActor, action: CalendarSourceAction, source: CalendarSource, broker: Broker, lease: LegacyTokenLease, events: GoogleEvent[], incremental: boolean, token: string | null, invalidate = false) {
      return transaction(actor, source.space_id, action, async tx => {
        await broker.assertCurrent(tx, lease); await current(tx, source);
        if (invalidate) await tx.query("UPDATE space_calendar_events SET status='canceled',removed_at=now(),updated_at=now() WHERE source_id=$1 AND removed_at IS NULL", [source.id]);
        for (const event of events) {
          if (!event.id) continue;
          const fingerprint = eventFingerprint(event);
          let times;
          try { times = await eventTimes(tx, event, source.timezone); }
          catch (error) {
            if (event.status !== "cancelled") throw error;
            await tx.query("UPDATE space_calendar_events SET status='canceled',removed_at=now(),updated_at=now() WHERE source_id=$1 AND external_event_id=$2", [source.id, event.id]);
          }
          if (times) {
            const status = event.status === "cancelled" ? "canceled" : ["confirmed", "tentative", "canceled"].includes(event.status) ? event.status : "confirmed";
            const optionalDate = (value: string) => value && Number.isFinite(Date.parse(value)) && !value.startsWith("0001-01-01T00:00:00") ? value : null;
            await tx.query(`INSERT INTO space_calendar_events(id,space_id,source_id,provider,external_event_id,fingerprint,title,description,location,meeting_url,organizer,
              starts_at,ends_at,all_day,timezone,status,provider_created_at,provider_updated_at)
              VALUES($1,$2,$3,'google',$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15,$16,$17)
              ON CONFLICT(source_id,external_event_id) DO UPDATE SET fingerprint=EXCLUDED.fingerprint,title=EXCLUDED.title,description=EXCLUDED.description,
              location=EXCLUDED.location,meeting_url=EXCLUDED.meeting_url,organizer=EXCLUDED.organizer,starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at,
              all_day=EXCLUDED.all_day,timezone=EXCLUDED.timezone,status=EXCLUDED.status,provider_created_at=EXCLUDED.provider_created_at,
              provider_updated_at=EXCLUDED.provider_updated_at,removed_at=NULL,updated_at=now()`,
            [`calendar_event_${randomUUID()}`, source.space_id, source.id, event.id, fingerprint, event.summary, event.description, event.location,
              event.hangoutLink || (event.htmlLink.startsWith("https://") ? event.htmlLink : ""), JSON.stringify(event.organizer ?? {}), times.startsAt, times.endsAt, times.allDay, times.timezone, status, optionalDate(event.created), optionalDate(event.updated)]);
          }
          if (incremental) await claimCalendarWorkflows(tx, source, event, fingerprint);
        }
        const result = (await tx.query<CalendarSource>(`UPDATE space_calendar_sources SET sync_token=CASE WHEN $2::text IS NULL THEN sync_token ELSE $2 END,
          status=CASE WHEN $2::text IS NULL OR $3 THEN status ELSE 'active' END,last_error_code='',
          last_reconciled_at=CASE WHEN $2::text IS NOT NULL AND NOT $3 THEN now() ELSE last_reconciled_at END,updated_at=clock_timestamp()
          WHERE id=$1 AND native_sync_lease_id=$4::uuid AND native_sync_lease_until>clock_timestamp() RETURNING ${sourceColumns}`, [source.id, token, invalidate, source.native_sync_lease_id])).rows[0];
        if (!result) throw new SpaceError("version_conflict"); return result;
      });
    },
    failed(actor: SpaceActor, action: CalendarSourceAction, source: CalendarSource, code: string) {
      return transaction(actor, source.space_id, action, async tx => {
        await tx.query(`UPDATE space_calendar_sources SET status='needs_attention',last_error_code=$4,updated_at=clock_timestamp()
          WHERE id=$1 AND space_id=$2 AND updated_at=$3::timestamptz AND disabled_at IS NULL AND status<>'disabled' AND execution_owner='hono' AND native_sync_lease_id=$5::uuid AND native_sync_lease_until>clock_timestamp()`, [source.id, source.space_id, source.revision, code, source.native_sync_lease_id]);
      });
    },
  };
}
