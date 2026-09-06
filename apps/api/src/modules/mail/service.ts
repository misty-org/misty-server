import type { MailAccount } from "@misty/contracts";
import { AppSessionRevoked } from "../app-runtime/repository.js";
import type { SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import { ConnectionTokenError, type ConnectionTokenLease, type createConnectionTokenBroker } from "../connections/token-broker.js";
import { type createMailRepository, type MailConnection } from "./repository.js";
import { createMailReader, type MailReader, type MailReaderFactory } from "./providers/reader.js";
import type { ThreadQuery } from "./providers/threads.js";
import { MailError, mailError } from "./errors.js";
import { createMailActionWriter, type MailActionWriterFactory, type ThreadChanges } from "./providers/actions.js";
import { createMailDraftService } from "./draft-service.js";
import type { MailDraftWriterFactory } from "./providers/drafts.js";

export function createMailService(options: { repository: ReturnType<typeof createMailRepository>; broker: ReturnType<typeof createConnectionTokenBroker> | null; reader?: MailReaderFactory; writer?: MailActionWriterFactory; draftWriter?: MailDraftWriterFactory }) {
  const reader = options.reader ?? createMailReader;
  const writer = options.writer ?? createMailActionWriter;
  const acquire = async (actor: SpaceActor, id: string, signal: AbortSignal) => {
    if (!options.broker) throw new ConnectionTokenError("not_configured");
    return options.broker.acquire(actor, id, "mailRead", signal);
  };
  const recordFailure = async (actor: SpaceActor, lease: ConnectionTokenLease | undefined, error: unknown) => {
    if (lease && mailError(error).code === "mail_provider_authorization_failed") await options.broker!.reportAuthorizationFailure(actor, lease);
  };
  const fallback = (account: MailConnection, error: unknown): MailAccount => ({ connection_id: account.id, provider: account.provider, account_id: account.account_id,
    email: account.account_display.includes("@") ? account.account_display : "", display_name: account.account_display, total: 0, unread: 0,
    status: !account.status || account.status === "active" ? "needs_attention" : account.status, error_code: account.last_error_code || mailError(error).code });
  const read = async <T>(actor: SpaceActor, connectionId: string, requestSignal: AbortSignal, operation: (provider: MailReader, signal: AbortSignal) => Promise<T>) => {
    const controller = new AbortController(), signal = AbortSignal.any([requestSignal, controller.signal, AbortSignal.timeout(45000)]); let lease: ConnectionTokenLease | undefined;
    try {
      lease = await acquire(actor, connectionId, signal);
      const result = await operation(reader(lease), signal);
      if (signal.aborted) throw new MailError("mail_provider_unavailable");
      await options.broker!.assertCurrent(actor, lease); return result;
    } catch (error) { await recordFailure(actor, lease, error); throw error; }
    finally { controller.abort(); }
  };
  return {
    ...createMailDraftService(options),
    modifyThread: async (actor: SpaceActor, connectionId: string, id: string, changes: ThreadChanges, requestSignal: AbortSignal) => {
      if (!options.broker) throw new ConnectionTokenError("not_configured");
      const controller = new AbortController(), signal = AbortSignal.any([requestSignal, controller.signal, AbortSignal.timeout(45000)]);
      let lease: ConnectionTokenLease | undefined, auditId: string | undefined, attempted = false, completionAttempted = false;
      try {
        lease = await options.broker.acquire(actor, connectionId, "mailWrite", signal);
        auditId = await options.repository.audit.begin(actor, { connectionId: lease.connectionId, action: "thread_modify", targetType: "thread", targetId: id, source: "user", confirmed: true });
        const currentLease = lease;
        const provider = writer(lease, (operation) => options.broker!.whileCurrent(actor, currentLease, async () => {
          signal.throwIfAborted(); attempted = true; return operation();
        }));
        const result = await provider.modifyThread(id, changes, signal);
        completionAttempted = true; await options.repository.audit.finish(actor.userId, auditId, true);
        await options.broker.assertCurrent(actor, lease); return result;
      } catch (error) {
        if (auditId && !completionAttempted) await options.repository.audit.finish(actor.userId, auditId, false, attempted ? "mail_operation_incomplete" : mailError(error).code);
        await recordFailure(actor, lease, error); throw error;
      } finally { controller.abort(); }
    },
    accounts: async (actor: SpaceActor, requestSignal: AbortSignal) => {
      const accounts = await options.repository.list(actor), results: Array<MailAccount | null> = Array(accounts.length).fill(null);
      const controller = new AbortController(), signal = AbortSignal.any([requestSignal, controller.signal, AbortSignal.timeout(45000)]); let next = 0;
      try {
        await Promise.all(Array.from({ length: Math.min(4, accounts.length) }, async () => {
          while (next < accounts.length && !signal.aborted) {
            const index = next++, account = accounts[index]!; let lease: ConnectionTokenLease | undefined;
            try {
              lease = await acquire(actor, account.id, signal); const provider = reader(lease), profile = await provider.account(signal);
              // Keep Microsoft identities without an Outlook/Exchange mailbox
              // visible as needing attention instead of repeatedly fetching mail.
              if (account.provider === "microsoft") await provider.folders(signal);
              await options.broker!.assertCurrent(actor, lease);
              results[index] = { connection_id: account.id, provider: account.provider, account_id: account.account_id,
                email: profile.email, display_name: profile.displayName.trim() || account.account_display, total: profile.total, unread: profile.unread };
            } catch (error) {
              if (error instanceof AppSessionRevoked || error instanceof SpaceError && ["not_authenticated", "forbidden"].includes(error.code)) throw error;
              if (error instanceof SpaceError && error.code === "not_found" || error instanceof ConnectionTokenError && error.code === "credential_changed") continue;
              await recordFailure(actor, lease, error); results[index] = fallback(account, error);
            }
          }
        }));
        if (signal.aborted) throw new MailError("mail_provider_unavailable");
        await options.repository.assertReader(actor);
        return { accounts: results.filter((account): account is MailAccount => account !== null) };
      } finally { controller.abort(); }
    },
    folders: (actor: SpaceActor, connectionId: string, signal: AbortSignal) => read(actor, connectionId, signal, async (provider, current) => ({ folders: await provider.folders(current) })),
    threads: (actor: SpaceActor, connectionId: string, query: ThreadQuery, signal: AbortSignal) => read(actor, connectionId, signal, (provider, current) => provider.threads(query, current)),
    thread: (actor: SpaceActor, connectionId: string, id: string, signal: AbortSignal) => read(actor, connectionId, signal, async (provider, current) => ({ thread: await provider.thread(id, current) })),
  };
}
