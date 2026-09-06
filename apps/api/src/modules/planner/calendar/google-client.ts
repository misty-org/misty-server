import { createHash } from "node:crypto";
import { z } from "zod";
import type { PoolClient } from "pg";
import type { LegacyTokenLease } from "../../connections/legacy-token-broker.js";
import { CalendarProviderError } from "./source-model.js";

const text = z.string().nullable().optional().transform(value => value ?? "");
const time = z.object({ date: text, dateTime: text, timeZone: text }).nullable().optional().transform(value => value ?? { date: "", dateTime: "", timeZone: "" });
const eventSchema = z.object({ id: text, etag: text, status: text, summary: text, description: text, location: text,
  htmlLink: text, hangoutLink: text, organizer: z.unknown().optional().transform(value => value ?? null), start: time, end: time, created: text, updated: text });
export type GoogleEvent = z.infer<typeof eventSchema>;
export const eventPageSchema = z.object({ items: z.array(eventSchema).nullable().optional().transform(value => value ?? []), nextPageToken: text, nextSyncToken: text });
const calendarSchema = z.object({ id: text, summary: text, timeZone: text, primary: z.boolean().nullable().optional().transform(value => value ?? false), accessRole: text });
const calendarPageSchema = z.object({ items: z.array(calendarSchema).nullable().optional().transform(value => value ?? []), nextPageToken: text });
export type GoogleCalendar = z.infer<typeof calendarSchema>;

export function createGoogleCalendarClient(fetcher: typeof fetch = fetch) {
  const request = async (lease: LegacyTokenLease, parts: string[], query: URLSearchParams, signal: AbortSignal, body?: unknown) => {
    if (parts.some(part => part === "." || part === "..")) throw new CalendarProviderError();
    const url = new URL(`https://www.googleapis.com/calendar/v3/${parts.map(encodeURIComponent).join("/")}`); url.search = query.toString();
    try {
      const response = await fetcher(url, { method: body === undefined ? "GET" : "POST", headers: { Authorization: `${lease.tokenType} ${lease.accessToken}`, Accept: "application/json", ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]), redirect: "error" });
      if (!response.ok) { await response.body?.cancel(); throw new CalendarProviderError(response.status); }
      const reader = response.body?.getReader(); if (!reader) throw new CalendarProviderError();
      let bytes = 0; const chunks: Uint8Array[] = [];
      try {
        for (;;) {
          const next = await reader.read(); if (next.done) break;
          bytes += next.value.byteLength; if (bytes > 4 * 1024 * 1024) throw new CalendarProviderError(); chunks.push(next.value);
        }
      } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
      return JSON.parse(Buffer.concat(chunks, bytes).toString()) as unknown;
    } catch (error) { if (error instanceof CalendarProviderError) throw error; throw new CalendarProviderError(); }
  };
  return {
    async watch(lease: LegacyTokenLease, calendarId: string, channel: { id: string; token: string; address: string; expiration: number }, signal: AbortSignal) {
      const raw = await request(lease, ["calendars", calendarId, "events", "watch"], new URLSearchParams(), signal, { ...channel, type: "web_hook" });
      const parsed = z.object({ id: z.string().min(1), resourceId: z.string().min(1), expiration: z.string().regex(/^\d+$/).optional() }).safeParse(raw);
      if (!parsed.success || parsed.data.id !== channel.id) throw new CalendarProviderError();
      const expiry = parsed.data.expiration ? Number(parsed.data.expiration) : channel.expiration;
      if (!Number.isSafeInteger(expiry) || expiry <= Date.now() || !Number.isFinite(new Date(expiry).getTime())) throw new CalendarProviderError();
      return { resourceId: parsed.data.resourceId, expiresAt: new Date(expiry) };
    },
    async calendars(lease: LegacyTokenLease, signal: AbortSignal) {
      const calendars: GoogleCalendar[] = []; let pageToken = "";
      for (let count = 0; count < 20; count++) {
        const query = new URLSearchParams({ maxResults: "250", showDeleted: "false" }); if (pageToken) query.set("pageToken", pageToken);
        const page = calendarPageSchema.safeParse(await request(lease, ["users", "me", "calendarList"], query, signal));
        if (!page.success) throw new CalendarProviderError();
        for (const calendar of page.data.items) if (calendar.id && calendar.accessRole !== "freeBusyReader") calendars.push({ ...calendar, timeZone: calendar.timeZone || "UTC" });
        if (!page.data.nextPageToken) return calendars;
        pageToken = page.data.nextPageToken;
      }
      throw new CalendarProviderError();
    },
    async events(lease: LegacyTokenLease, calendarId: string, syncToken: string, pageToken: string, signal: AbortSignal) {
      const query = new URLSearchParams({ maxResults: "2500", showDeleted: "true", singleEvents: "true" });
      if (syncToken) query.set("syncToken", syncToken); if (pageToken) query.set("pageToken", pageToken);
      const page = eventPageSchema.safeParse(await request(lease, ["calendars", calendarId, "events"], query, signal));
      if (!page.success) throw new CalendarProviderError(); return page.data;
    },
  };
}
// Match Go's typed event envelope, field order and JSON HTML escaping so legacy
// fingerprints/claim IDs remain stable across the handover.
export function eventFingerprint(event: GoogleEvent) {
  const keys: (keyof GoogleEvent)[] = ["id", "etag", "status", "summary", "description", "location", "htmlLink", "hangoutLink", "organizer", "start", "end", "created", "updated"];
  const raw = `{${keys.map(key => `${JSON.stringify(key)}:${JSON.stringify(event[key])}`).join(",")}}`
    .replace(/[<>&\u2028\u2029]/g, char => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return createHash("sha256").update(raw).digest("hex");
}
export async function eventTimes(tx: PoolClient, event: GoogleEvent, fallback: string) {
  let timezone = event.start.timeZone || fallback || "UTC";
  if (event.start.dateTime && event.end.dateTime) {
    if (!z.iso.datetime({ offset: true }).safeParse(event.start.dateTime).success || !z.iso.datetime({ offset: true }).safeParse(event.end.dateTime).success) throw new CalendarProviderError();
    const valid = (await tx.query<{ valid: boolean }>("SELECT $2::timestamptz >= $1::timestamptz AS valid", [event.start.dateTime, event.end.dateTime])).rows[0]!.valid;
    if (!valid) throw new CalendarProviderError();
    return { startsAt: event.start.dateTime, endsAt: event.end.dateTime, allDay: false, timezone };
  }
  try { new Intl.DateTimeFormat("en", { timeZone: timezone }); } catch { timezone = "UTC"; }
  if (!z.iso.date().safeParse(event.start.date).success || !z.iso.date().safeParse(event.end.date).success || event.end.date < event.start.date) throw new CalendarProviderError();
  const result = (await tx.query<{ starts_at: Date; ends_at: Date }>("SELECT $1::date::timestamp AT TIME ZONE $3 AS starts_at,$2::date::timestamp AT TIME ZONE $3 AS ends_at", [event.start.date, event.end.date, timezone])).rows[0]!;
  return { startsAt: result.starts_at, endsAt: result.ends_at, allDay: true, timezone };
}
