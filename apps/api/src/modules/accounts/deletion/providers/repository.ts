import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../../packages/database/src/transaction.js";
import type { DeletionJob } from "../jobs.js";
import { inventoryProviderResources, readProviderResource, verifyProviderSource, type ProviderResource } from "./resources.js";
import type { DeletionCredential } from "./credentials.js";

export type ProviderOutcome = "token_revoked" | "token_inactive" | "webhook_deleted" | "local_credential_erased" | "subscription_inactive";
export type ProviderWork = { resource: ProviderResource; credential: DeletionCredential | null };
async function lockStage(tx: PoolClient, job: DeletionJob) {
  if (job.step !== "providers") return false;
  await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
  // Space writers precede account writers throughout the native API.
  await tx.query(`SELECT id FROM spaces WHERE id IN (
    SELECT space_id FROM space_integrations WHERE connected_by_user_id=$1
    UNION SELECT space_id FROM space_provider_credentials WHERE user_id=$1
    UNION SELECT space_id FROM provider_shared_resources WHERE published_by_user_id=$1
    UNION SELECT b.space_id FROM figma_space_bindings b JOIN connected_accounts c ON c.id=b.connection_id WHERE c.user_id=$1
  ) ORDER BY id FOR UPDATE`, [job.user_id]);
  if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND license_id=$2 AND lifecycle_state='pending_deletion' FOR UPDATE", [job.user_id, job.license_id])).rowCount) return false;
  if (!(await tx.query("SELECT id FROM account_deletion_requests WHERE id=$1 AND user_id=$2 AND cleanup_owner='native' AND status='processing' FOR UPDATE", [job.request_id, job.user_id])).rowCount) return false;
  return !!(await tx.query("SELECT request_id FROM account_deletion_steps WHERE request_id=$1 AND step='providers' AND state='processing' AND lease_token=$2 AND lease_expires_at>clock_timestamp() FOR UPDATE", [job.request_id, job.lease_token])).rowCount;
}
async function releaseStep(tx: PoolClient, job: DeletionJob, delay: number, complete = false) {
  const changed = await tx.query(`UPDATE account_deletion_steps SET state=$3,lease_token=NULL,lease_expires_at=NULL,available_at=clock_timestamp()+make_interval(secs=>$4),
    completed_at=CASE WHEN $3='completed' THEN now() ELSE NULL END,result=CASE WHEN $3='completed' THEN '{"outcome":"provider_resources_completed"}'::jsonb ELSE '{}'::jsonb END,
    last_error_code='',updated_at=now() WHERE request_id=$1 AND step='providers' AND state='processing' AND lease_token=$2 AND lease_expires_at>clock_timestamp()`,
  [job.request_id, job.lease_token, complete ? "completed" : "pending", delay]);
  if (!changed.rowCount) throw new Error("Provider cleanup lease expired");
}
async function finishLocalDependencies(tx: PoolClient, job: DeletionJob) {
  await tx.query(`UPDATE figma_webhook_subscriptions SET status='disabled',last_error_code='',updated_at=now() WHERE binding_id IN
    (SELECT b.id FROM figma_space_bindings b JOIN connected_accounts c ON c.id=b.connection_id WHERE c.user_id=$1)`, [job.user_id]);
  await tx.query(`UPDATE provider_shared_resources SET status='disabled',updated_at=now() WHERE published_by_user_id=$1
    OR integration_id IN(SELECT id FROM space_integrations WHERE connected_by_user_id=$1)
    OR integration_id IN(SELECT integration_id FROM space_provider_credentials WHERE user_id=$1)
    OR id IN(SELECT b.shared_resource_id FROM figma_space_bindings b JOIN connected_accounts c ON c.id=b.connection_id WHERE c.user_id=$1)`, [job.user_id]);
  await tx.query(`UPDATE space_integrations SET status='disabled',credential_reference='account_deleted',updated_at=now() WHERE connected_by_user_id=$1
    OR id IN(SELECT integration_id FROM space_provider_credentials WHERE user_id=$1)
    OR id IN(SELECT b.integration_id FROM figma_space_bindings b JOIN connected_accounts c ON c.id=b.connection_id WHERE c.user_id=$1)`, [job.user_id]);
  await tx.query(`UPDATE figma_space_bindings SET status='disabled',disabled_at=COALESCE(disabled_at,now()),last_error_code='',updated_at=now()
    WHERE connection_id IN(SELECT id FROM connected_accounts WHERE user_id=$1)`, [job.user_id]);
  await tx.query("UPDATE provider_subscriptions SET status='disabled',updated_at=now() WHERE user_id=$1", [job.user_id]);
  await tx.query("DELETE FROM provider_event_inbox WHERE user_id=$1", [job.user_id]);
}
export function createDeletionProviderRepository(pool: Pool) {
  return {
    async prepare(job: DeletionJob): Promise<ProviderWork | null> {
      return withTransaction(pool, async tx => {
        if (!await lockStage(tx, job)) return null;
        await inventoryProviderResources(tx, job.request_id, job.user_id);
        const resource = await readProviderResource(tx, job.request_id);
        if (!resource) {
          const pending = (await tx.query("SELECT 1 FROM account_deletion_provider_resources WHERE request_id=$1 AND state='pending' LIMIT 1", [job.request_id])).rowCount;
          if (!pending) {
            await finishLocalDependencies(tx, job);
            await tx.query(`UPDATE account_deletion_requests SET provider_revocation_status=COALESCE((SELECT jsonb_object_agg(provider,'completed'::text)
              FROM (SELECT DISTINCT provider FROM account_deletion_provider_resources WHERE request_id=$1) providers),'{}'::jsonb),
              last_error_code=CASE WHEN last_error_code='providers_cleanup_unavailable' THEN '' ELSE last_error_code END,updated_at=now() WHERE id=$1`, [job.request_id]);
          }
          await releaseStep(tx, job, pending ? 30 : 0, !pending); return null;
        }
        await verifyProviderSource(tx, job.user_id, resource);
        let source = resource;
        if (resource.credential_kind && resource.credential_id) {
          const found = (await tx.query<ProviderResource>("SELECT * FROM account_deletion_provider_resources WHERE request_id=$1 AND kind=$2 AND resource_id=$3 AND state='pending' FOR UPDATE", [job.request_id, resource.credential_kind, resource.credential_id])).rows[0];
          if (!found || found.provider !== resource.provider) throw new Error("Missing cleanup credential owner");
          await verifyProviderSource(tx, job.user_id, found); source = found;
        }
        const credential = source.ciphertext && source.nonce && source.key_version !== null && source.credential_format ?
          { format: source.credential_format, provider: source.provider, ciphertext: source.ciphertext, nonce: source.nonce, keyVersion: source.key_version } : null;
        return { resource, credential };
      }, { mode: "service" });
    },
    async complete(job: DeletionJob, work: ProviderWork, outcome: ProviderOutcome): Promise<boolean> {
      return withTransaction(pool, async tx => {
        if (!await lockStage(tx, job)) return false;
        const resource = (await tx.query<ProviderResource>("SELECT * FROM account_deletion_provider_resources WHERE request_id=$1 AND kind=$2 AND resource_id=$3 AND state='pending' FOR UPDATE", [job.request_id, work.resource.kind, work.resource.resource_id])).rows[0];
        if (!resource || resource.source_fingerprint !== work.resource.source_fingerprint) return false;
        await verifyProviderSource(tx, job.user_id, resource);
        if (resource.kind === "figma_webhook") {
          if (outcome !== "webhook_deleted") throw new Error("Invalid webhook receipt");
          await tx.query("UPDATE figma_webhook_subscriptions SET status='disabled',last_error_code='',updated_at=now() WHERE id=$1", [resource.resource_id]);
        } else if (resource.kind === "provider_subscription") {
          if (outcome !== "subscription_inactive") throw new Error("Invalid subscription receipt");
          await tx.query("UPDATE provider_subscriptions SET status='disabled',updated_at=now() WHERE id=$1 AND user_id=$2", [resource.resource_id, job.user_id]);
        } else {
          if (!["token_revoked", "token_inactive", "local_credential_erased"].includes(outcome)) throw new Error("Invalid credential receipt");
          const dependency = await tx.query("SELECT 1 FROM account_deletion_provider_resources WHERE request_id=$1 AND credential_kind=$2 AND credential_id=$3 AND state<>'completed' LIMIT 1", [job.request_id, resource.kind, resource.resource_id]);
          if (dependency.rowCount) throw new Error("Unresolved credential dependency");
          const table = resource.kind === "connected" ? "connected_accounts" : resource.kind === "cloud" ? "cloud_connections" : "space_provider_credentials";
          const fields = resource.kind === "integration" ? "ciphertext=''::bytea,nonce=''::bytea" : "credential_ciphertext=''::bytea,credential_nonce=''::bytea,status='revoked',last_error_code=''";
          await tx.query(`UPDATE ${table} SET ${fields},revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE id=$1 AND user_id=$2`, [resource.resource_id, job.user_id]);
        }
        await tx.query("UPDATE account_deletion_provider_resources SET state='completed',outcome=$4,ciphertext=NULL,nonce=NULL,execution_ciphertext=NULL,execution_nonce=NULL,execution_key_version=NULL,last_error_code='',completed_at=now(),updated_at=now() WHERE request_id=$1 AND kind=$2 AND resource_id=$3", [job.request_id, resource.kind, resource.resource_id, outcome]);
        await releaseStep(tx, job, 0); return true;
      }, { mode: "service" });
    },
    async checkpointDropbox(job: DeletionJob, work: ProviderWork, encrypted: { ciphertext: Buffer; nonce: Buffer; keyVersion: number }, clientIdHash: string): Promise<boolean> {
      return withTransaction(pool, async tx => {
        if (!await lockStage(tx, job)) return false;
        const resource = (await tx.query<ProviderResource>("SELECT * FROM account_deletion_provider_resources WHERE request_id=$1 AND kind=$2 AND resource_id=$3 AND state='pending' FOR UPDATE", [job.request_id, work.resource.kind, work.resource.resource_id])).rows[0];
        if (!resource || resource.provider !== "dropbox" || !["connected", "cloud", "integration"].includes(resource.kind) || resource.source_fingerprint !== work.resource.source_fingerprint) return false;
        if (resource.execution_client_id_hash && resource.execution_client_id_hash !== clientIdHash) throw new Error("Cleanup OAuth client changed");
        await verifyProviderSource(tx, job.user_id, resource);
        const saved = await tx.query(`UPDATE account_deletion_provider_resources SET execution_ciphertext=$4,execution_nonce=$5,execution_key_version=$6,execution_client_id_hash=$7,updated_at=now()
          WHERE request_id=$1 AND kind=$2 AND resource_id=$3 AND EXISTS(SELECT 1 FROM account_deletion_steps WHERE request_id=$1 AND step='providers'
            AND state='processing' AND lease_token=$8 AND lease_expires_at>clock_timestamp())`,
        [job.request_id, resource.kind, resource.resource_id, encrypted.ciphertext, encrypted.nonce, encrypted.keyVersion, clientIdHash, job.lease_token]);
        return !!saved.rowCount;
      }, { mode: "service" });
    },
    async fail(job: DeletionJob, work: ProviderWork): Promise<boolean> {
      return withTransaction(pool, async tx => {
        if (!await lockStage(tx, job)) return false;
        await tx.query(`UPDATE account_deletion_provider_resources SET attempts=attempts+1,last_error_code='provider_cleanup_unavailable',
          available_at=clock_timestamp()+make_interval(secs=>LEAST(3600,15*power(2,LEAST(attempts,8)))::integer),updated_at=now()
          WHERE request_id=$1 AND kind=$2 AND resource_id=$3 AND state='pending' AND source_fingerprint=$4`, [job.request_id, work.resource.kind, work.resource.resource_id, work.resource.source_fingerprint]);
        await tx.query("UPDATE account_deletion_requests SET last_error_code='providers_cleanup_unavailable',updated_at=now() WHERE id=$1", [job.request_id]);
        await releaseStep(tx, job, 0); return true;
      }, { mode: "service" });
    },
  };
}
