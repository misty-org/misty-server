import { z } from "zod";
import type { PoolClient } from "pg";
import { clientInteger, SpaceError, trimSpace } from "../../spaces/model.js";

const text = z.string().nullable().optional().transform(value => value ?? "");
export const nativeEventColumns = `e.id,e.space_id,e.title,e.description,e.location,e.starts_at,e.ends_at,e.all_day,e.timezone,e.status,
  e.audience_kind,e.audience_conversation_id,e.created_by_user_id,e.created_by_agent_id,e.source_run_id,e.version,e.created_at,e.updated_at`;
export const calendarAudience = (alias: "e" | "r") => `(${alias}.audience_kind='space' OR EXISTS(
  SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
  WHERE cm.conversation_id=${alias}.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4 AND c.space_id=${alias}.space_id))`;
export function nativeEventResponse(row: Record<string, unknown>) {
  const result = { ...row, provider: "misty", source_id: "misty", origin: "native", external_event_id: "", fingerprint: "",
    meeting_url: "", organizer: null, version: clientInteger(row.version as string) } as Record<string, unknown>;
  for (const key of ["audience_conversation_id", "created_by_agent_id", "source_run_id"]) if (!result[key]) delete result[key];
  return result;
}
export function calendarInput(raw: unknown, update = false) {
  const parsed = z.object({ title: text, description: text, location: text, timezone: text, status: text,
    starts_at: z.iso.datetime({ offset: true }), ends_at: z.iso.datetime({ offset: true }), all_day: z.boolean().nullable().optional(),
    version: z.number().int().safe().positive().optional(), audience_kind: text, audience_conversation_id: text }).safeParse(raw);
  if (!parsed.success || update && !parsed.data.version) throw new SpaceError("invalid_request");
  const input = parsed.data;
  for (const field of ["title", "description", "location", "timezone"] as const) input[field] = trimSpace(input[field]);
  input.timezone ||= "UTC"; input.status ||= "confirmed";
  if (!input.title || [...input.title].length > 240 || [...input.description].length > 20000 || [...input.location].length > 1000
    || !["confirmed", "tentative", "canceled"].includes(input.status)) throw new SpaceError("invalid_request");
  try { new Intl.DateTimeFormat("en", { timeZone: input.timezone }); } catch { throw new SpaceError("invalid_request"); }
  return { ...input, all_day: input.all_day ?? false };
}
export async function calendarRange(tx: PoolClient, raw: Record<string, string | undefined>) {
  const parsed = z.object({ from: z.iso.datetime({ offset: true }), to: z.iso.datetime({ offset: true }) }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request");
  const { from, to } = parsed.data;
  if (!(await tx.query<{ valid: boolean }>("SELECT $2::timestamptz>$1::timestamptz AND $2::timestamptz-$1::timestamptz<=interval '370 days' AS valid", [from, to])).rows[0]!.valid) throw new SpaceError("invalid_request");
  return { from, to };
}
export async function validateEventTimes(tx: PoolClient, starts: string, ends: string) {
  if (!(await tx.query<{ valid: boolean }>("SELECT $2::timestamptz >= $1::timestamptz AND $1::timestamptz<>'0001-01-01T00:00:00Z'::timestamptz AS valid", [starts, ends])).rows[0]!.valid) throw new SpaceError("invalid_request");
}
