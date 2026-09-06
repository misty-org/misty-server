import { z } from "zod";
import type { MailThreadAction } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { MailError } from "../errors.js";
import { createMailTransport } from "./transport.js";
import { graphNextToken } from "./pagination.js";

export type ThreadChanges = Pick<MailThreadAction, "read" | "archived" | "starred">;
export type MailWriteGuard = <T>(operation: () => Promise<T>) => Promise<T>;
const providerId = z.string().regex(/^[\x21-\x7e]{1,320}$/).refine((id) => id !== "." && id !== "..");
const pageSchema = z.object({ value: z.array(z.object({ id: providerId, conversationId: providerId })).nullish().transform((rows) => rows ?? []),
  "@odata.nextLink": z.string().nullish().transform((value) => value ?? "") });

export function createMailActionWriter(lease: ConnectionTokenLease, guard: MailWriteGuard, fetcher: typeof fetch = fetch) {
  const request = createMailTransport(lease, fetcher), google = lease.account.provider === "google";
  const write = (parts: string[], method: "POST" | "PATCH", body: unknown, signal: AbortSignal) => guard(() => request(parts, new URLSearchParams(), signal, { method, body, discardResponse: true }));
  return {
    modifyThread: async (id: string, changes: ThreadChanges, signal: AbortSignal) => {
      if (!providerId.safeParse(id).success || Object.values(changes).every((value) => value === undefined)) throw new MailError("mail_invalid_request");
      const added_labels: string[] = [], removed_labels: string[] = [];
      if (changes.read !== undefined) (changes.read ? removed_labels : added_labels).push("UNREAD");
      if (changes.archived !== undefined) (google ? changes.archived ? removed_labels : added_labels : changes.archived ? added_labels : removed_labels).push(google ? "INBOX" : "ARCHIVED");
      if (changes.starred !== undefined) (changes.starred ? added_labels : removed_labels).push("STARRED");
      if (google) {
        await write(["users", "me", "threads", id, "modify"], "POST", {
          ...(added_labels.length ? { addLabelIds: added_labels } : {}), ...(removed_labels.length ? { removeLabelIds: removed_labels } : {}),
        }, signal);
      } else {
        // Finish pagination before moving messages; moving while paging can skip
        // messages. Validate every ID/conversation before the first mutation.
        const query = new URLSearchParams({ $select: "id,conversationId", $top: "100", $filter: `conversationId eq '${id.replaceAll("'", "''")}'` });
        const ids = new Set<string>(), tokens = new Set<string>(); let finished = false, rows = 0;
        for (let index = 0; index < 20; index++) {
          const parsed = pageSchema.safeParse(await request(["me", "messages"], query, signal));
          if (!parsed.success) throw new MailError("mail_provider_unavailable");
          rows += parsed.data.value.length; if (rows > 500) throw new MailError("mail_response_too_large");
          for (const message of parsed.data.value) {
            if (message.conversationId !== id) throw new MailError("mail_provider_unavailable");
            ids.add(message.id);
          }
          const token = graphNextToken(parsed.data["@odata.nextLink"]);
          if (!token) { finished = true; break; }
          if (tokens.has(token)) throw new MailError("mail_response_too_large");
          tokens.add(token); query.set("$skiptoken", token);
        }
        if (!finished) throw new MailError("mail_response_too_large");
        if (!ids.size) throw new MailError("mail_provider_item_not_found");
        for (const message of ids) {
          const patch = { ...(changes.read === undefined ? {} : { isRead: changes.read }),
            ...(changes.starred === undefined ? {} : { flag: { flagStatus: changes.starred ? "flagged" : "notFlagged" } }) };
          if (Object.keys(patch).length) await write(["me", "messages", message], "PATCH", patch, signal);
          if (changes.archived !== undefined) await write(["me", "messages", message, "move"], "POST", { destinationId: changes.archived ? "archive" : "inbox" }, signal);
        }
      }
      return { thread_id: id, added_labels, removed_labels };
    },
  };
}
export type MailActionWriterFactory = (lease: ConnectionTokenLease, guard: MailWriteGuard) => ReturnType<typeof createMailActionWriter>;
