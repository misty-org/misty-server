import type { ConnectionTokenLease } from "../../connections/token-broker.js";
import type { MailWriteGuard } from "./actions.js";
import { createGmailDraftWriter } from "./gmail-drafts.js";
import { createGraphDraftWriter } from "./graph-drafts.js";

export function createMailDraftWriter(lease: ConnectionTokenLease, guard: MailWriteGuard, created: (id: string) => Promise<void>, fetcher: typeof fetch = fetch) {
  return lease.account.provider === "google" ? createGmailDraftWriter(lease, guard, fetcher, created) : createGraphDraftWriter(lease, guard, fetcher, created);
}
export type MailDraftWriterFactory = (lease: ConnectionTokenLease, guard: MailWriteGuard, created: (id: string) => Promise<void>) => ReturnType<typeof createMailDraftWriter>;
