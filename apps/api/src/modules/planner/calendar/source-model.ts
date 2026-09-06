import { z } from "zod";
import { SpaceError, trimSpace } from "../../spaces/model.js";

export { ProviderRequestError as CalendarProviderError } from "../../connections/provider-error.js";
export type CalendarSource = {
  id: string; space_id: string; integration_id: string; connected_by_user_id: string; provider: string;
  external_calendar_id: string; display_name: string; timezone: string; sync_token: string;
  watch_channel_id: string; watch_resource_id: string; watch_token_hash: string; watch_expires_at: Date | null;
  status: string; last_error_code: string; last_reconciled_at: Date | null; disabled_at: Date | null;
  created_at: Date; updated_at: Date; revision: string; execution_owner: "go" | "hono";
  native_sync_requested: string; native_sync_completed: string; native_sync_available_at: Date;
  native_sync_lease_id: string | null; native_sync_lease_until: Date | null;
};
export const sourceColumns = `id,space_id,integration_id,connected_by_user_id,provider,external_calendar_id,display_name,timezone,
  sync_token,watch_channel_id,watch_resource_id,watch_token_hash,watch_expires_at,status,last_error_code,last_reconciled_at,disabled_at,created_at,updated_at,updated_at::text AS revision,execution_owner,native_sync_requested::text,native_sync_completed::text,native_sync_available_at,native_sync_lease_id,native_sync_lease_until`;
export function sourceResponse(source: CalendarSource) {
  const { sync_token: _cursor, watch_channel_id: _channel, watch_resource_id: _resource, watch_token_hash: _hash, revision: _revision, execution_owner: _owner, native_sync_requested: _requested, native_sync_completed: _completed,
    native_sync_available_at: _available, native_sync_lease_id: _lease, native_sync_lease_until: _until, ...result } = source;
  const dto = result as Record<string, unknown>;
  for (const key of ["watch_expires_at", "last_error_code", "last_reconciled_at", "disabled_at"]) if (!dto[key]) delete dto[key];
  return dto;
}
export const mistySource = (spaceId: string) => ({ id: "misty", space_id: spaceId, integration_id: "", connected_by_user_id: "", provider: "misty",
  external_calendar_id: "misty", display_name: "Misty", timezone: "UTC", status: "active", created_at: "0001-01-01T00:00:00Z", updated_at: "0001-01-01T00:00:00Z" });
const text = z.string().nullable().optional().transform(value => trimSpace(value ?? ""));
const inputSchema = z.object({ integration_id: text, external_calendar_id: text, display_name: text, timezone: text });
export function sourceInput(raw: unknown) {
  const parsed = inputSchema.safeParse(raw);
  if (!parsed.success || !parsed.data.integration_id || !parsed.data.external_calendar_id || [parsed.data.external_calendar_id, parsed.data.integration_id].some(value => value === "." || value === "..")) throw new SpaceError("invalid_request");
  if ([...parsed.data.display_name].length > 240 || parsed.data.timezone.length > 80) throw new SpaceError("invalid_request");
  if (parsed.data.timezone) try { new Intl.DateTimeFormat("en", { timeZone: parsed.data.timezone }); } catch { throw new SpaceError("invalid_request"); }
  return parsed.data;
}
