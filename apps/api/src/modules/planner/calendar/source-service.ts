import { createHash, randomBytes } from "node:crypto";
import { z } from "zod";
import type { SpaceActor } from "../../spaces/access.js";
import { SpaceError } from "../../spaces/model.js";
import type { createLegacyTokenBroker } from "../../connections/legacy-token-broker.js";
import { createGoogleCalendarClient } from "./google-client.js";
import type { createCalendarSourceRepository } from "./source-repository.js";
import { CalendarProviderError, mistySource, sourceInput, sourceResponse, type CalendarSource } from "./source-model.js";
import type { CalendarSourceAction } from "./source-access.js";

export function createCalendarSourceService(options: { repository: ReturnType<typeof createCalendarSourceRepository>; broker: ReturnType<typeof createLegacyTokenBroker> | null; fetcher?: typeof fetch; watchAddress?: string | null }) {
  const repository = options.repository, google = createGoogleCalendarClient(options.fetcher);
  const broker = () => { if (!options.broker) throw new CalendarProviderError(); return options.broker; };
  const signalFor = (signal: AbortSignal) => AbortSignal.any([signal, AbortSignal.timeout(120000)]);
  const synchronize = async (actor: SpaceActor, action: CalendarSourceAction, initial: CalendarSource, signal: AbortSignal) => {
    let source = await repository.claim(actor, action, initial);
    if (!source) return false;
    const claimed = source, generation = source.native_sync_requested;
    let success = false;
    try {
      const vault = broker(), lease = await vault.acquire({ userId: source.connected_by_user_id }, source.space_id, source.integration_id, signal);
      let cursor = source.sync_token, pageToken = "", incremental = !!cursor;
      for (let pageNumber = 0; pageNumber < 100; pageNumber++) {
        signal.throwIfAborted();
        let page;
        try { page = await google.events(lease, source.external_calendar_id, cursor, pageToken, signal); }
        catch (error) {
          if (!(error instanceof CalendarProviderError) || error.status !== 410 || !cursor) throw error;
          source = await repository.applyPage(actor, action, source, vault, lease, [], false, "", true);
          cursor = ""; pageToken = ""; incremental = false; continue;
        }
        if (!page.nextPageToken && !page.nextSyncToken) throw new CalendarProviderError();
        source = await repository.applyPage(actor, action, source, vault, lease, page.items, incremental, page.nextPageToken ? null : page.nextSyncToken);
        if (!page.nextPageToken) {
          if (options.watchAddress && (!source.watch_resource_id || !source.watch_expires_at || source.watch_expires_at.getTime() < Date.now() + 86400000)) {
            const id = `gcal_${randomBytes(24).toString("base64url")}`, token = randomBytes(32).toString("base64url"), expiration = Date.now() + 6 * 86400000;
            source = await repository.beginWatch(actor, action, source, vault, lease, { id, hash: createHash("sha256").update(token).digest("hex"), expiresAt: new Date(expiration) });
            const watch = await google.watch(lease, source.external_calendar_id, { id, token, address: options.watchAddress, expiration }, signal);
            source = await repository.finishWatch(actor, action, source, vault, lease, watch.resourceId, watch.expiresAt);
          }
          success = true; return true;
        }
        pageToken = page.nextPageToken;
      }
      throw new CalendarProviderError();
    } catch (error) {
      await repository.failed(actor, action, source, error instanceof CalendarProviderError ? error.code : "provider_error");
      return false;
    } finally { await repository.finish(claimed, generation, success); }
  };
  return {
    async reconcile(source: CalendarSource, signal: AbortSignal) {
      return synchronize({ userId: source.connected_by_user_id }, "list", source, signalFor(signal));
    },
    async list(actor: SpaceActor, spaceId: string) { return { sources: [mistySource(spaceId), ...(await repository.list(actor, spaceId)).map(sourceResponse)] }; },
    async available(actor: SpaceActor, spaceId: string, integrationId: string, signal: AbortSignal) {
      await repository.list(actor, spaceId, "available");
      if (!integrationId.trim()) throw new SpaceError("invalid_request");
      const vault = broker(), lease = await vault.acquire(actor, spaceId, integrationId.trim(), signalFor(signal));
      const calendars = await google.calendars(lease, signalFor(signal));
      await repository.transaction(actor, spaceId, "available", tx => vault.assertCurrent(tx, lease));
      return { calendars };
    },
    async create(actor: SpaceActor, spaceId: string, raw: unknown, signal: AbortSignal) {
      const input = sourceInput(raw), bounded = signalFor(signal);
      await repository.list(actor, spaceId, "manage");
      const vault = broker(), lease = await vault.acquire(actor, spaceId, input.integration_id, bounded, "calendar.write");
      const calendar = (await google.calendars(lease, bounded)).find(item => item.id === input.external_calendar_id);
      if (!calendar) throw new SpaceError("invalid_request");
      const normalized = sourceInput({ ...input, display_name: input.display_name || calendar.summary, timezone: input.timezone || calendar.timeZone });
      if (!normalized.display_name) throw new SpaceError("invalid_request");
      const source = await repository.create(actor, spaceId, normalized, vault, lease);
      await synchronize(actor, "manage", source, bounded); return sourceResponse(source);
    },
    disable: repository.disable,
    async sync(actor: SpaceActor, spaceId: string, raw: unknown, signal: AbortSignal) {
      const body = z.object({ source_id: z.string().nullable().optional() }).safeParse(raw);
      if (!body.success) throw new SpaceError("invalid_request");
      const sources = await repository.list(actor, spaceId, "sync"), bounded = signalFor(signal);
      for (const source of sources) if (source.status !== "disabled" && (!body.data.source_id || source.id === body.data.source_id)) await synchronize(actor, "sync", source, bounded);
      return { tasks: [], sources: [mistySource(spaceId), ...(await repository.list(actor, spaceId, "sync")).map(sourceResponse)], synced_at: new Date().toISOString() };
    },
  };
}
