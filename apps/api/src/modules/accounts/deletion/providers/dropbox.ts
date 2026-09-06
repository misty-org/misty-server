import { createHash } from "node:crypto";
import type { ConnectionCipher } from "../../../connections/credentials.js";
import type { ConnectionOAuthClients } from "../../../connections/config.js";
import type { DeletionJob } from "../jobs.js";
import { ProviderCleanupError, withDeletionCredential, type CleanupTokens } from "./credentials.js";
import type { createDropboxCleanupRefresh } from "./dropbox-refresh.js";
import type { createProviderCleanupGateway } from "./gateway.js";
import type { createDeletionProviderRepository, ProviderOutcome, ProviderWork } from "./repository.js";

export function createDropboxDeletion(options: { cipher: ConnectionCipher; clients: ConnectionOAuthClients;
  refresh: ReturnType<typeof createDropboxCleanupRefresh>; gateway: ReturnType<typeof createProviderCleanupGateway>;
  repository: ReturnType<typeof createDeletionProviderRepository> }) {
  return async (job: DeletionJob, work: ProviderWork, original: CleanupTokens, signal: AbortSignal): Promise<ProviderOutcome> => {
    if (!original.refreshToken) return options.gateway({ kind: "dropbox-access-token", token: original.accessToken }, signal);
    const client = original.customClient ?? options.clients.dropbox;
    if (!client) throw new ProviderCleanupError("provider_cleanup_unavailable");
    const clientIdHash = createHash("sha256").update(client.clientId).digest("hex"), resource = work.resource;
    if (resource.execution_client_id_hash && resource.execution_client_id_hash !== clientIdHash) throw new ProviderCleanupError("provider_cleanup_ambiguous");
    let current = original, proven = false;
    if (resource.execution_ciphertext && resource.execution_nonce && resource.execution_key_version !== null) {
      if (!resource.execution_client_id_hash) throw new ProviderCleanupError("provider_cleanup_ambiguous");
      current = await withDeletionCredential(options.cipher, { format: "connected", provider: "dropbox", ciphertext: resource.execution_ciphertext,
        nonce: resource.execution_nonce, keyVersion: resource.execution_key_version }, async tokens => tokens);
      if (!current.refreshToken) throw new ProviderCleanupError("provider_credential_unreadable");
      proven = true;
      try { return await options.gateway({ kind: "dropbox-access-token", token: current.accessToken }, signal); }
      catch (error) { if (!(error instanceof ProviderCleanupError) || error.code !== "provider_access_token_rejected") throw error; }
    }
    let refreshed: Awaited<ReturnType<typeof options.refresh>>;
    try { refreshed = await options.refresh({ refreshToken: current.refreshToken, ...client }, signal); }
    catch (error) {
      if (error instanceof ProviderCleanupError && error.code === "provider_refresh_rejected") {
        // A successful, checkpointed refresh bound this exact OAuth client and
        // token lineage. An initial invalid_grant could instead be wrong config.
        if (proven) return "token_inactive";
        throw new ProviderCleanupError("provider_cleanup_ambiguous");
      }
      throw error;
    }
    signal.throwIfAborted();
    const plaintext = Buffer.from(JSON.stringify(refreshed));
    const encrypted = (() => { try { return options.cipher.encrypt("dropbox", plaintext); } finally { plaintext.fill(0); } })();
    try {
      if (!await options.repository.checkpointDropbox(job, work, encrypted, clientIdHash)) throw new ProviderCleanupError("provider_cleanup_unavailable");
      signal.throwIfAborted();
      return await options.gateway({ kind: "dropbox-access-token", token: refreshed.access_token }, signal);
    } finally { encrypted.ciphertext.fill(0); encrypted.nonce.fill(0); }
  };
}
