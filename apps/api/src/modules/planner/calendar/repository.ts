import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError, trimSpace } from "../../spaces/model.js";
import { requirePlannerActor } from "../access.js";
import { calendarAudience, calendarInput, calendarRange, nativeEventColumns, nativeEventResponse, validateEventTimes } from "./model.js";
import { loadAgenda } from "./agenda.js";

async function eventNotification(tx: PoolClient, actor: SpaceActor, spaceId: string, id: string, type: string, payload: unknown) {
  const event = (await tx.query<{ id: string }>(`INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
    VALUES($1,$2,$3,$4,$5::jsonb) RETURNING id`, [spaceId, `calendar.event.${type}`, actor.userId, id, JSON.stringify(payload)])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [event.id]);
}
export function createCalendarRepository(pool: Pool) {
  function transaction<T>(actor: SpaceActor, spaceId: string, scope: "calendar.read" | "calendar.write" | "tasks.read", operation: (tx: PoolClient) => Promise<T>) {
    return withTransaction(pool, async tx => {
      await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
      await requirePlannerActor(tx, actor, spaceId, scope, scope === "calendar.write");
      return operation(tx);
    }, { mode: "service" });
  }
  return {
    list(actor: SpaceActor, spaceId: string, query: Record<string, string | undefined>) {
      return transaction(actor, spaceId, "calendar.read", async tx => {
        const { from, to } = await calendarRange(tx, query);
        const imported = (await tx.query(`SELECT id,space_id,source_id,provider,external_event_id,fingerprint,title,description,location,meeting_url,organizer,
          starts_at,ends_at,all_day,timezone,status,provider_created_at,provider_updated_at,removed_at,created_at,updated_at
          FROM space_calendar_events WHERE space_id=$1 AND starts_at<$2::timestamptz AND ends_at>$3::timestamptz
          AND removed_at IS NULL ORDER BY starts_at,id`, [spaceId, to, from])).rows;
        for (const event of imported) for (const key of ["provider_created_at", "provider_updated_at", "removed_at"]) if (event[key] === null) delete event[key];
        const native = (await tx.query(`SELECT ${nativeEventColumns} FROM space_native_calendar_events e
          WHERE e.space_id=$1 AND e.starts_at<$2::timestamptz AND e.ends_at>$3::timestamptz AND e.archived_at IS NULL
          AND ${calendarAudience("e")} ORDER BY e.starts_at,e.id`, [spaceId, to, from, actor.userId])).rows.map(nativeEventResponse);
        return { events: [...imported, ...native] };
      });
    },
    agenda(actor: SpaceActor, spaceId: string, query: Record<string, string | undefined>) {
      return transaction(actor, spaceId, "tasks.read", async tx => {
        const { from, to } = await calendarRange(tx, query);
        return loadAgenda(tx, spaceId, actor.userId, from, to);
      });
    },
    create(actor: SpaceActor, spaceId: string, raw: unknown) {
      const input = calendarInput(raw), id = `native_event_${randomUUID()}`;
      return transaction(actor, spaceId, "calendar.write", async tx => {
        await validateEventTimes(tx, input.starts_at, input.ends_at);
        const kind = trimSpace(input.audience_kind) || "space", conversation = trimSpace(input.audience_conversation_id);
        if (kind !== "space" && kind !== "conversation" || kind === "conversation" && !conversation || kind === "space" && conversation) throw new SpaceError("invalid_request");
        if (kind === "conversation" && !(await tx.query(`SELECT cm.user_id FROM space_conversation_members cm
          JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.user_id=$2
          AND cm.actor_kind='person' AND c.space_id=$3 FOR SHARE OF cm`, [conversation, actor.userId, spaceId])).rowCount) throw new SpaceError("forbidden");
        const row = (await tx.query(`INSERT INTO space_native_calendar_events AS e(id,space_id,title,description,location,starts_at,ends_at,
          all_day,timezone,status,audience_kind,audience_conversation_id,created_by_user_id)
          VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),$13) RETURNING ${nativeEventColumns}`,
        [id, spaceId, input.title, input.description, input.location, input.starts_at, input.ends_at, input.all_day,
          input.timezone, input.status, kind, kind === "conversation" ? conversation : "", actor.userId])).rows[0]!;
        const event = nativeEventResponse(row); await eventNotification(tx, actor, spaceId, id, "created", { event }); return event;
      });
    },
    update(actor: SpaceActor, spaceId: string, id: string, raw: unknown) {
      const input = calendarInput(raw, true);
      return transaction(actor, spaceId, "calendar.write", async tx => {
        await validateEventTimes(tx, input.starts_at, input.ends_at);
        const row = (await tx.query(`UPDATE space_native_calendar_events AS e SET title=$5,description=$6,location=$7,
          starts_at=$8,ends_at=$9,all_day=$10,timezone=$11,status=$12,version=e.version+1,updated_at=now()
          WHERE e.id=$1 AND e.space_id=$2 AND e.version=$3 AND e.archived_at IS NULL AND ${calendarAudience("e")}
          RETURNING ${nativeEventColumns}`, [id, spaceId, input.version, actor.userId, input.title, input.description, input.location,
          input.starts_at, input.ends_at, input.all_day, input.timezone, input.status])).rows[0];
        if (!row) throw new SpaceError("version_conflict");
        const event = nativeEventResponse(row); await eventNotification(tx, actor, spaceId, id, "updated", { event }); return event;
      });
    },
    archive(actor: SpaceActor, spaceId: string, id: string, version: string) {
      if (!/^[+-]?\d+$/.test(version) || BigInt(version) < -9223372036854775808n || BigInt(version) > 9223372036854775807n) throw new SpaceError("invalid_request");
      return transaction(actor, spaceId, "calendar.write", async tx => {
        if (!(await tx.query(`UPDATE space_native_calendar_events AS e SET archived_at=now(),version=e.version+1,updated_at=now()
          WHERE e.id=$1 AND e.space_id=$2 AND e.version=$3 AND e.archived_at IS NULL AND ${calendarAudience("e")}`,
        [id, spaceId, version, actor.userId])).rowCount) throw new SpaceError("version_conflict");
        await eventNotification(tx, actor, spaceId, id, "archived", null);
      });
    },
  };
}
