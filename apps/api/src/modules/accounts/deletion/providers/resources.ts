import type { PoolClient } from "pg";

export type ProviderResourceKind = "connected" | "cloud" | "integration" | "figma_webhook" | "provider_subscription";
export type ProviderResource = { kind: ProviderResourceKind; resource_id: string; provider: string; credential_kind: "connected" | "cloud" | "integration" | null;
  credential_id: string | null; credential_format: "connected" | "legacy" | null; ciphertext: Buffer | null; nonce: Buffer | null; key_version: number | null;
  source_fingerprint: string; details: Record<string, unknown>; state: "pending" | "completed"; attempts: number;
  execution_ciphertext: Buffer | null; execution_nonce: Buffer | null; execution_key_version: number | null; execution_client_id_hash: string | null };

// Preserve an encrypted snapshot before any remote effect. Source IDs, ownership
// and original bytes remain evidence; no token or provider payload enters details.
export async function inventoryProviderResources(tx: PoolClient, requestId: string, userId: string) {
  const conflict = await tx.query(`SELECT 1 FROM provider_subscriptions s JOIN space_provider_credentials c ON c.integration_id=s.integration_id
    WHERE (s.user_id=$1 OR c.user_id=$1) AND s.user_id<>c.user_id LIMIT 1`, [userId]);
  if (conflict.rowCount) throw new Error("Provider subscription ownership conflict");
  const reappeared = await tx.query(`SELECT 1 FROM account_deletion_provider_resources r JOIN (
    SELECT 'connected' AS kind,id FROM connected_accounts WHERE user_id=$2 AND (revoked_at IS NULL OR octet_length(credential_ciphertext)>0)
    UNION ALL SELECT 'cloud',id FROM cloud_connections WHERE user_id=$2 AND (revoked_at IS NULL OR octet_length(credential_ciphertext)>0)
    UNION ALL SELECT 'integration',id FROM space_provider_credentials WHERE user_id=$2 AND (revoked_at IS NULL OR octet_length(ciphertext)>0)
  ) current ON current.kind=r.kind AND current.id=r.resource_id WHERE r.request_id=$1 AND r.state='completed' LIMIT 1`, [requestId, userId]);
  if (reappeared.rowCount) throw new Error("Completed provider credential reappeared");
  await tx.query(`INSERT INTO account_deletion_provider_resources(request_id,kind,resource_id,provider,credential_format,ciphertext,nonce,key_version,source_fingerprint)
    SELECT $1,kind,id,provider,format,ciphertext,nonce,key_version,encode(sha256(convert_to(kind||':'||id||':'||provider||':'||key_version::text||':'||encode(ciphertext,'hex')||':'||encode(nonce,'hex'),'UTF8')),'hex')
    FROM (
      SELECT 'connected' AS kind,id,provider,'connected' AS format,credential_ciphertext AS ciphertext,credential_nonce AS nonce,key_version FROM connected_accounts WHERE user_id=$2 AND (revoked_at IS NULL OR octet_length(credential_ciphertext)>0)
      UNION ALL SELECT 'cloud',id,provider,'legacy',credential_ciphertext,credential_nonce,key_version FROM cloud_connections WHERE user_id=$2 AND (revoked_at IS NULL OR octet_length(credential_ciphertext)>0)
      UNION ALL SELECT 'integration',id,provider,'legacy',ciphertext,nonce,key_version FROM space_provider_credentials WHERE user_id=$2 AND (revoked_at IS NULL OR octet_length(ciphertext)>0)
    ) sources ON CONFLICT DO NOTHING`, [requestId, userId]);
  await tx.query(`INSERT INTO account_deletion_provider_resources(request_id,kind,resource_id,provider,credential_kind,credential_id,source_fingerprint,details)
    SELECT $1,'figma_webhook',h.id,'figma','connected',b.connection_id,
      encode(sha256(convert_to(h.id||':'||h.webhook_id||':'||b.id||':'||b.connection_id,'UTF8')),'hex'),jsonb_build_object('webhookId',h.webhook_id,'bindingId',b.id)
    FROM figma_webhook_subscriptions h JOIN figma_space_bindings b ON b.id=h.binding_id
      JOIN connected_accounts c ON c.id=b.connection_id WHERE c.user_id=$2
    ON CONFLICT DO NOTHING`, [requestId, userId]);
  await tx.query(`INSERT INTO account_deletion_provider_resources(request_id,kind,resource_id,provider,credential_kind,credential_id,source_fingerprint,details)
    SELECT $1,'provider_subscription',s.id,s.provider,CASE WHEN c.id IS NULL THEN NULL ELSE 'integration' END,c.id,
      encode(sha256(convert_to(s.id||':'||s.provider||':'||s.external_subscription_id||':'||s.integration_id,'UTF8')),'hex'),
      jsonb_build_object('externalId',s.external_subscription_id,'integrationId',s.integration_id,'resourceKey',s.resource_key,'expiresAt',s.expires_at)
    FROM provider_subscriptions s LEFT JOIN space_provider_credentials c ON c.integration_id=s.integration_id AND c.user_id=s.user_id
    WHERE s.user_id=$2 ON CONFLICT DO NOTHING`, [requestId, userId]);
}

export async function readProviderResource(tx: PoolClient, requestId: string): Promise<ProviderResource | null> {
  const result = await tx.query<ProviderResource>(`SELECT * FROM account_deletion_provider_resources r WHERE request_id=$1 AND state='pending' AND available_at<=clock_timestamp()
    AND (kind IN ('figma_webhook','provider_subscription') OR NOT EXISTS(SELECT 1 FROM account_deletion_provider_resources dependency
      WHERE dependency.request_id=r.request_id AND dependency.state<>'completed' AND dependency.credential_kind=r.kind AND dependency.credential_id=r.resource_id))
    ORDER BY CASE WHEN kind IN ('figma_webhook','provider_subscription') THEN 0 ELSE 1 END,available_at,kind,resource_id LIMIT 1 FOR UPDATE`, [requestId]);
  return result.rows[0] ?? null;
}

/** Read current source ownership before accepting any remote acknowledgement.
 * Missing or changed source data requires reconciliation, never blind erasure. */
export async function verifyProviderSource(tx: PoolClient, userId: string, resource: ProviderResource) {
  let fingerprint: string | undefined;
  if (["connected", "cloud", "integration"].includes(resource.kind)) {
    const table = resource.kind === "connected" ? "connected_accounts" : resource.kind === "cloud" ? "cloud_connections" : "space_provider_credentials";
    const bytes = resource.kind === "integration" ? "ciphertext" : "credential_ciphertext", nonce = resource.kind === "integration" ? "nonce" : "credential_nonce";
    fingerprint = (await tx.query<{ fingerprint: string }>(`SELECT encode(sha256(convert_to($3||':'||id||':'||provider||':'||key_version::text||':'||encode(${bytes},'hex')||':'||encode(${nonce},'hex'),'UTF8')),'hex') AS fingerprint
      FROM ${table} WHERE id=$1 AND user_id=$2 FOR UPDATE`, [resource.resource_id, userId, resource.kind])).rows[0]?.fingerprint;
  } else if (resource.kind === "figma_webhook") {
    fingerprint = (await tx.query<{ fingerprint: string }>(`SELECT encode(sha256(convert_to(h.id||':'||h.webhook_id||':'||b.id||':'||b.connection_id,'UTF8')),'hex') AS fingerprint
      FROM figma_webhook_subscriptions h JOIN figma_space_bindings b ON b.id=h.binding_id JOIN connected_accounts c ON c.id=b.connection_id
      WHERE h.id=$1 AND c.user_id=$2 FOR UPDATE OF h,b,c`, [resource.resource_id, userId])).rows[0]?.fingerprint;
  } else {
    fingerprint = (await tx.query<{ fingerprint: string }>(`SELECT encode(sha256(convert_to(id||':'||provider||':'||external_subscription_id||':'||integration_id,'UTF8')),'hex') AS fingerprint
      FROM provider_subscriptions WHERE id=$1 AND user_id=$2 FOR UPDATE`, [resource.resource_id, userId])).rows[0]?.fingerprint;
  }
  if (fingerprint !== resource.source_fingerprint) throw new Error("Provider cleanup source changed");
}
