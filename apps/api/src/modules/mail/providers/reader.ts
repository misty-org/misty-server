import { z } from "zod";
import type { MailFolder } from "@misty/contracts";
import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import { MailError } from "../errors.js";
import { createMailTransport } from "./transport.js";
import { createThreadReader } from "./threads.js";
import { cleanHeader } from "./format.js";
import { graphNextToken } from "./pagination.js";

const text = z.string().nullish().transform((value) => value ?? "");
const count = z.number().int().nonnegative().nullish().transform((value) => value ?? 0);
const labels = z.object({ labels: z.array(z.object({ id: text, name: text, type: text, threadsTotal: count, threadsUnread: count,
  color: z.object({ textColor: text, backgroundColor: text }).nullish() })).nullish() });
const graphPage = z.object({ value: z.array(z.object({ id: text, displayName: text, wellKnownName: text, totalItemCount: count, unreadItemCount: count })).nullish(), "@odata.nextLink": text });
const googleKinds: Record<string, string> = { INBOX: "inbox", SENT: "sent", DRAFT: "drafts", TRASH: "trash", SPAM: "spam", STARRED: "starred", IMPORTANT: "important" };
const graphKinds: Record<string, string> = { inbox: "inbox", sentitems: "sent", "sent items": "sent", sent: "sent", drafts: "drafts", deleteditems: "trash", "deleted items": "trash", trash: "trash", junkemail: "spam", "junk email": "spam", spam: "spam" };
const folderKind = (kinds: Record<string, string>, name: string) => Object.hasOwn(kinds, name) ? kinds[name]! : "custom";
export type MailProfile = { email: string; displayName: string; total: number; unread: number };
export type MailReader = { account(signal: AbortSignal): Promise<MailProfile>; folders(signal: AbortSignal): Promise<MailFolder[]> } & ReturnType<typeof createThreadReader>;
export type MailReaderFactory = (lease: ConnectionTokenLease) => MailReader;

export function createMailReader(lease: ConnectionTokenLease, fetcher: typeof fetch = fetch): MailReader {
  const request = createMailTransport(lease, fetcher), google = lease.account.provider === "google";
  const parse = <T>(schema: z.ZodType<T>, input: unknown): T => {
    const result = schema.safeParse(input); if (!result.success) throw new MailError("mail_provider_unavailable"); return result.data;
  };
  return {
    ...createThreadReader(lease, request),
    account: async (signal) => {
      if (google) {
        const profile = parse(z.object({ emailAddress: z.string().trim().min(1), messagesTotal: count }), await request(["users", "me", "profile"], new URLSearchParams(), signal));
        return { email: profile.emailAddress, displayName: "", total: profile.messagesTotal, unread: 0 };
      }
      const profile = parse(z.object({ id: z.string().trim().min(1), displayName: text, mail: text, userPrincipalName: text }),
        await request(["me"], new URLSearchParams({ $select: "id,displayName,mail,userPrincipalName" }), signal));
      const email = profile.mail.trim() || profile.userPrincipalName.trim(); if (!email) throw new MailError("mail_provider_unavailable");
      return { email, displayName: cleanHeader(profile.displayName), total: 0, unread: 0 };
    },
    folders: async (signal) => {
      if (google) {
        const page = parse(labels, await request(["users", "me", "labels"], new URLSearchParams(), signal));
        return (page.labels ?? []).filter((label) => !!label.id.trim()).map((label) => ({
          provider: "gmail", provider_id: label.id, account_id: lease.account.accountId, name: label.name, kind: folderKind(googleKinds, label.id), system: label.type === "system",
          total: label.threadsTotal, unread: label.threadsUnread,
          text_color: label.color?.textColor ?? "", background: label.color?.backgroundColor ?? "",
        }));
      }
      const folders: MailFolder[] = [], seen = new Set<string>();
      const query = new URLSearchParams({ $select: "id,displayName,wellKnownName,totalItemCount,unreadItemCount", $top: "100" });
      for (let index = 0; index < 20; index++) {
        const page = parse(graphPage, await request(["me", "mailFolders"], query, signal));
        for (const folder of page.value ?? []) {
          if (!folder.id.trim()) continue;
          const kind = folderKind(graphKinds, (folder.wellKnownName.trim() || folder.displayName.trim()).toLowerCase());
          folders.push({ provider: "outlook", provider_id: folder.id, account_id: lease.account.accountId, name: cleanHeader(folder.displayName), kind,
            system: !!folder.wellKnownName || kind !== "custom", total: folder.totalItemCount, unread: folder.unreadItemCount, text_color: "", background: "" });
          if (folders.length > 500) throw new MailError("mail_response_too_large");
        }
        const token = graphNextToken(page["@odata.nextLink"]);
        if (!token) return folders;
        if (seen.has(token)) throw new MailError("mail_response_too_large"); seen.add(token);
        // Only the opaque token is reused. Provider-supplied paths and other
        // query parameters never change the endpoint or selected fields.
        query.set("$skiptoken", token);
      }
      throw new MailError("mail_response_too_large");
    },
  };
}
