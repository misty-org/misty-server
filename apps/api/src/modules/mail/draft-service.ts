import type { MailDraft, MailDraftInput } from "@misty/contracts";
import type { SpaceActor } from "../spaces/access.js";
import { ConnectionTokenError, type ConnectionTokenLease, type createConnectionTokenBroker } from "../connections/token-broker.js";
import type { createMailRepository } from "./repository.js";
import { createMailAudit, type MailAuditIntent } from "./audit.js";
import { MailError, mailError } from "./errors.js";
import { createMailDraftWriter, type MailDraftWriterFactory } from "./providers/drafts.js";
import { prepareDraft } from "./providers/draft-input.js";

export function createMailDraftService(options: { repository: ReturnType<typeof createMailRepository>; broker: ReturnType<typeof createConnectionTokenBroker> | null; draftWriter?: MailDraftWriterFactory }) {
  const writer = options.draftWriter ?? createMailDraftWriter;
  const mutate = async <T>(actor: SpaceActor, intent: MailAuditIntent, signal: AbortSignal,
    operation: (provider: ReturnType<MailDraftWriterFactory>, signal: AbortSignal) => Promise<T>, target?: (result: T) => string) => {
    const broker = options.broker; if (!broker) throw new ConnectionTokenError("not_configured");
    const controller = new AbortController(), deadline = AbortSignal.any([signal, controller.signal, AbortSignal.timeout(90000)]); let lease: ConnectionTokenLease | undefined;
    try {
      lease = await broker.acquire(actor, intent.connectionId, "mailWrite", deadline); const currentLease = lease;
      return await options.repository.draftConnection(lease.connectionId, intent.action === "draft_create" ? "" : intent.targetId, deadline, async (pool, current) => {
        const audit = createMailAudit(pool); let auditId: string | undefined, attempted = false, writes = 0, completionAttempted = false;
        try {
          await broker.whileCurrent(actor, currentLease, async () => {}, pool);
          if (intent.action !== "draft_create") await audit.assertDraftSettled(actor.userId, currentLease.connectionId, intent.targetId);
          auditId = await audit.begin(actor, { ...intent, connectionId: currentLease.connectionId });
          if (intent.action === "draft_send" && !intent.confirmed) throw new MailError("mail_confirmation_required");
          const provider = writer(currentLease, (write) => broker.whileCurrent(actor, currentLease, async () => {
            current.throwIfAborted(); attempted = true; const result = await write(); writes++; return result;
          }, pool), (id) => audit.identifyDraft(actor.userId, auditId!, id));
          const result = await operation(provider, current);
          completionAttempted = true; await audit.finish(actor.userId, auditId, true, "", target?.(result));
          await broker.whileCurrent(actor, currentLease, async () => {}, pool); return result;
        } catch (error) {
          // A definitive refusal before any successful write is safe to correct.
          // Lost responses and failures after earlier writes require reconciliation.
          const refused = error instanceof MailError && error.providerStatus !== undefined && [400,401,403,404,409,422,429].includes(error.providerStatus);
          const uncertain = attempted && (writes > 0 || !refused);
          if (auditId && !completionAttempted) await audit.finish(actor.userId, auditId, false, uncertain ? "mail_operation_incomplete" : mailError(error).code);
          throw error;
        }
      });
    } catch (error) {
      if (lease && mailError(error).code === "mail_provider_authorization_failed") await broker.reportAuthorizationFailure(actor, lease);
      throw error;
    } finally { controller.abort(); }
  };
  const draftIntent = (connectionId: string, action: MailAuditIntent["action"], targetId: string): MailAuditIntent => ({ connectionId, action, targetType: "draft", targetId, source: "user", confirmed: true });
  const target = (result: { draft: MailDraft }) => result.draft.provider_id;
  return {
    createDraft: (actor: SpaceActor, input: MailDraftInput, signal: AbortSignal) => {
      const prepared = prepareDraft(input);
      return mutate(actor, draftIntent(input.connection_id, "draft_create", "new"), signal, async (provider, current) => ({ draft: await provider.createDraft(prepared, current) }), target);
    },
    updateDraft: (actor: SpaceActor, id: string, input: MailDraftInput, signal: AbortSignal) => {
      const prepared = prepareDraft(input);
      return mutate(actor, draftIntent(input.connection_id, "draft_update", id), signal, async (provider, current) => ({ draft: await provider.updateDraft(id, prepared, current) }), target);
    },
    sendDraft: (actor: SpaceActor, id: string, input: { connection_id: string; authoring_source: "user" | "ai"; confirmed: boolean }, signal: AbortSignal) => {
      const confirmed = input.confirmed || !actor.appSession && input.authoring_source === "user";
      return mutate(actor, { ...draftIntent(input.connection_id, "draft_send", id), source: input.authoring_source, confirmed }, signal,
        async (provider, current) => ({ message: await provider.sendDraft(id, current) }));
    },
  };
}
