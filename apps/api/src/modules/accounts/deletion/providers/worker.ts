import type { Pool } from "pg";
import type { ConnectionCipher } from "../../../connections/credentials.js";
import type { ConnectionOAuthClients } from "../../../connections/config.js";
import { createAccountDeletionJobs, type DeletionJob } from "../jobs.js";
import { createDeletionProviderRepository, type ProviderOutcome, type ProviderWork } from "./repository.js";
import { ProviderCleanupError, withDeletionCredential } from "./credentials.js";
import type { createProviderCleanupGateway } from "./gateway.js";
import type { createDropboxCleanupRefresh } from "./dropbox-refresh.js";
import { createDropboxDeletion } from "./dropbox.js";

/** Stage factory only. Unsupported/ambiguous effects retain their encrypted
 * resource and keep the provider stage pending rather than certifying erasure. */
export function createDeletionProviderWorker(options: { pool: Pool; cipher: ConnectionCipher; clients: ConnectionOAuthClients; gateway: ReturnType<typeof createProviderCleanupGateway>; dropboxRefresh: ReturnType<typeof createDropboxCleanupRefresh> }) {
  const jobs = createAccountDeletionJobs(options.pool), repository = createDeletionProviderRepository(options.pool);
  const dropbox = createDropboxDeletion({ ...options, repository, refresh: options.dropboxRefresh });
  const effect = async (job: DeletionJob, work: ProviderWork, signal: AbortSignal): Promise<ProviderOutcome> => {
    if (work.resource.kind === "provider_subscription") {
      // Provider-specific remote cleanup and expiry verification must be added;
      // a local disabled flag alone cannot prove a remote subscription ended.
      throw new ProviderCleanupError("provider_cleanup_unavailable");
    }
    if (!work.credential) throw new ProviderCleanupError("provider_credential_unreadable");
    return withDeletionCredential(options.cipher, work.credential, async tokens => {
      const provider = work.resource.provider;
      if (work.resource.kind === "figma_webhook") {
        if (typeof work.resource.details.webhookId !== "string") throw new ProviderCleanupError("provider_cleanup_invalid_request");
        return options.gateway({ kind: "figma-webhook", token: tokens.accessToken, webhookId: work.resource.details.webhookId }, signal);
      }
      if (provider === "google" || provider === "drive") return options.gateway({ kind: "google-token", token: tokens.refreshToken || tokens.accessToken }, signal);
      if (provider === "discord") {
        const client = tokens.customClient ?? options.clients.discord;
        if (!client) throw new ProviderCleanupError("provider_cleanup_unavailable");
        return options.gateway({ kind: "discord-token", token: tokens.refreshToken || tokens.accessToken, tokenType: tokens.refreshToken ? "refresh_token" : "access_token", ...client }, signal);
      }
      if (provider === "dropbox") return dropbox(job, work, tokens, signal);
      // These supported delegated flows have no configured per-token revocation
      // operation. Figma dependencies are completed before its token is erased.
      if (["microsoft", "onedrive", "figma"].includes(provider)) return "local_credential_erased";
      throw new ProviderCleanupError("provider_cleanup_unavailable");
    });
  };
  return { async runOnce(requestSignal: AbortSignal = new AbortController().signal): Promise<boolean> {
    requestSignal.throwIfAborted(); const job = await jobs.claim("providers"); if (!job) return false;
    let work: ProviderWork | null = null;
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(45000)]);
    try {
      work = await repository.prepare(job); if (!work) return true;
      signal.throwIfAborted(); const outcome = await effect(job, work, signal); signal.throwIfAborted();
      await repository.complete(job, work, outcome);
    } catch {
      if (work) await repository.fail(job, work); else await jobs.retry(job);
    } finally {
      work?.credential?.ciphertext.fill(0); work?.credential?.nonce.fill(0);
      work?.resource.ciphertext?.fill(0); work?.resource.nonce?.fill(0);
      work?.resource.execution_ciphertext?.fill(0); work?.resource.execution_nonce?.fill(0);
    }
    return true;
  } };
}
