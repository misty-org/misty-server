import { createCipheriv, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../../packages/database/src/transaction.js";
import { createConnectionCipher } from "../../../connections/credentials.js";
import type { ConnectionOAuthClients } from "../../../connections/config.js";
import { createAccountDeletionJobs } from "../jobs.js";
import { createDeletionProviderRepository } from "./repository.js";
import { createDeletionProviderWorker } from "./worker.js";
import { createProviderCleanupGateway } from "./gateway.js";
import { createDropboxCleanupRefresh } from "./dropbox-refresh.js";
const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
const cipher = createConnectionCipher(Buffer.alloc(32, 19));
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_deletion_providers_test') THEN CREATE ROLE misty_deletion_providers_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_deletion_providers_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON account_deletion_provider_resources TO misty_deletion_providers_test;
    GRANT SELECT,UPDATE ON users,spaces,account_deletion_requests,account_deletion_steps,connected_accounts,cloud_connections,space_provider_credentials,
      space_integrations,provider_shared_resources,figma_space_bindings,figma_webhook_subscriptions,provider_subscriptions TO misty_deletion_providers_test;
    GRANT SELECT,DELETE ON provider_event_inbox TO misty_deletion_providers_test;
    GRANT SELECT ON space_members TO misty_deletion_providers_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_deletion_providers_test", max: 4 });
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("DELETE FROM account_deletion_requests WHERE user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  });
  users.length = 0; spaces.length = 0; domains.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const userId = `cleanup_${randomUUID().replaceAll("-", "").slice(0, 12)}`, licenseId = `license_${userId}`, requestId = `deletion_${randomUUID()}`; users.push(userId);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id,lifecycle_state) VALUES($1,$2,'test-only',$1,$3,'pending_deletion')", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [licenseId, userId]);
    await tx.query("INSERT INTO account_deletion_requests(id,user_id,status_token_hash,purge_after,cleanup_owner) VALUES($1,$2,$3,now()+interval '30 days','native')", [requestId, userId, randomUUID()]);
    await tx.query("INSERT INTO account_deletion_steps(request_id,step) SELECT $1,unnest(ARRAY['payments','providers','local','purge'])", [requestId]);
  });
  const add = async (provider = "google", refresh = "fixture-refresh") => {
    const id = `connection_${randomUUID()}`, encrypted = cipher.encrypt(provider, Buffer.from(JSON.stringify({ access_token: "fixture-access", ...(refresh ? { refresh_token: refresh } : {}) })));
    await admin.query("INSERT INTO connected_accounts(id,user_id,provider,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,$3,$4,$5,$6)", [id, userId, provider, id, encrypted.ciphertext, encrypted.nonce]); return { id, ...encrypted };
  };
  const worker = (fetcher: typeof fetch = async () => new Response(null, { status: 200 }), clients: ConnectionOAuthClients = { discord: { clientId: "fixture-client", clientSecret: "fixture-secret" } }) => createDeletionProviderWorker({ pool: application, cipher,
    clients, gateway: createProviderCleanupGateway(fetcher), dropboxRefresh: createDropboxCleanupRefresh(fetcher) });
  const resources = async () => (await admin.query("SELECT kind,resource_id,state,ciphertext,nonce,outcome,last_error_code,attempts FROM account_deletion_provider_resources WHERE request_id=$1 ORDER BY kind,resource_id", [requestId])).rows;
  const stage = async () => (await admin.query("SELECT state,last_error_code,result FROM account_deletion_steps WHERE request_id=$1 AND step='providers'", [requestId])).rows[0];
  const due = async () => { await admin.query("UPDATE account_deletion_provider_resources SET available_at=clock_timestamp() WHERE request_id=$1", [requestId]); await admin.query("UPDATE account_deletion_steps SET available_at=clock_timestamp() WHERE request_id=$1 AND step='providers'", [requestId]); };
  return { userId, requestId, add, worker, resources, stage, due };
}
it("acknowledges current and legacy Google credentials, erases retry bytes only after confirmation, and completes only the provider stage", async () => {
  const f = await fixture(); await f.add();
  const old = JSON.parse(await readFile(new URL("../../../../../../../docs/migration/fixtures/legacy-provider-credentials.json", import.meta.url), "utf8")).find((row: { provider: string }) => row.provider === "drive");
  await admin.query("INSERT INTO cloud_connections(id,user_id,provider,name,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,'drive','Old Drive','old-account',$3,$4)", [`cloud_${randomUUID()}`, f.userId, Buffer.from(old.ciphertext, "base64"), Buffer.from(old.nonce, "base64")]);
  const send = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 })), worker = f.worker(send);
  await worker.runOnce(); await worker.runOnce(); expect((await f.stage()).state).toBe("pending");
  await worker.runOnce(); expect(await f.stage()).toMatchObject({ state: "completed", result: { outcome: "provider_resources_completed" } });
  expect(await f.resources()).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "connected", state: "completed", ciphertext: null, nonce: null }), expect.objectContaining({ kind: "cloud", state: "completed", ciphertext: null, nonce: null })]));
  expect(send.mock.calls.every(([url]) => url === "https://oauth2.googleapis.com/revoke")).toBe(true);
  expect(send.mock.calls.map(([, init]) => new URLSearchParams(String(init!.body)).get("token")).sort()).toEqual(["fixture-drive", "fixture-refresh"].sort());
  expect((await admin.query("SELECT octet_length(credential_ciphertext) AS bytes,status FROM connected_accounts WHERE user_id=$1", [f.userId])).rows[0]).toEqual({ bytes: 0, status: "revoked" });
  expect(await createAccountDeletionJobs(application).claim("local")).toBeNull();
  await withTransaction(application, async tx => { expect((await tx.query("SELECT * FROM account_deletion_provider_resources WHERE request_id=$1", [f.requestId])).rowCount).toBe(0); }, { mode: "user", userId: f.userId });
});
it("recovers a lost Google revocation response without losing encrypted retry material", async () => {
  const f = await fixture(), connection = await f.add(); let revoked = false;
  const worker = f.worker(async () => { if (!revoked) { revoked = true; throw new Error("private provider diagnostics"); } return Response.json({ error: "invalid_token" }, { status: 400 }); });
  await worker.runOnce(); const first = (await f.resources())[0];
  expect(first).toMatchObject({ state: "pending", last_error_code: "provider_cleanup_unavailable", ciphertext: connection.ciphertext });
  expect((await admin.query("SELECT credential_ciphertext FROM connected_accounts WHERE id=$1", [connection.id])).rows[0].credential_ciphertext).toEqual(connection.ciphertext);
  await f.due(); await worker.runOnce(); expect((await f.resources())[0]).toMatchObject({ state: "completed", outcome: "token_inactive", ciphertext: null });
});
it("continues independent resources while unsupported providers and unconfigured Dropbox clients remain pending", async () => {
  const f = await fixture(); await f.add("unconfigured"); await f.add("dropbox"); await f.add("google");
  const send = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 })), worker = f.worker(send);
  for (let i = 0; i < 4; i++) await worker.runOnce();
  const resources = await f.resources(); expect(resources.filter(row => row.state === "completed")).toHaveLength(1); expect(resources.filter(row => row.ciphertext !== null)).toHaveLength(2);
  expect(send).toHaveBeenCalledTimes(1); expect((await f.stage()).state).toBe("pending");
});
it("rejects source replacement during remote I/O without erasing either old retry evidence or the new credential", async () => {
  const f = await fixture(), connection = await f.add(), replacement = cipher.encrypt("google", Buffer.from('{"access_token":"replacement"}'));
  await f.worker(async () => {
    await admin.query("UPDATE connected_accounts SET credential_ciphertext=$2,credential_nonce=$3 WHERE id=$1", [connection.id, replacement.ciphertext, replacement.nonce]);
    return new Response(null, { status: 200 });
  }).runOnce();
  expect((await f.resources())[0]).toMatchObject({ state: "pending", ciphertext: connection.ciphertext });
  expect((await admin.query("SELECT credential_ciphertext FROM connected_accounts WHERE id=$1", [connection.id])).rows[0].credential_ciphertext).toEqual(replacement.ciphertext);
});
it("does not certify completion if an already erased source credential reappears", async () => {
  const f = await fixture(), connection = await f.add(); const worker = f.worker(); await worker.runOnce();
  await admin.query("UPDATE connected_accounts SET credential_ciphertext=$2,credential_nonce=$3,revoked_at=NULL,status='active' WHERE id=$1", [connection.id, connection.ciphertext, connection.nonce]);
  await worker.runOnce(); expect(await f.stage()).toMatchObject({ state: "pending", last_error_code: "providers_cleanup_unavailable" });
});
it("rejects an expired/replaced worker fence before source erasure or resource acknowledgement", async () => {
  const f = await fixture(); await f.add(); const jobs = createAccountDeletionJobs(application), repository = createDeletionProviderRepository(application);
  const old = (await jobs.claim("providers"))!, first = (await repository.prepare(old))!;
  await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE request_id=$1 AND step='providers'", [f.requestId]);
  const current = (await jobs.claim("providers"))!, second = (await repository.prepare(current))!;
  expect(await repository.complete(old, first, "token_revoked")).toBe(false); expect(await repository.fail(old, first)).toBe(false);
  expect((await f.resources())[0].state).toBe("pending");
  expect(await repository.complete(current, second, "token_revoked")).toBe(true); expect((await f.resources())[0].state).toBe("completed");
});
it("keeps network I/O outside account locks and permits only one current provider-stage claim", async () => {
  const f = await fixture(); await f.add(); let entered!: () => void, release!: () => void;
  const started = new Promise<void>(resolve => { entered = resolve; }), waiting = new Promise<void>(resolve => { release = resolve; });
  const pending = f.worker(async () => { entered(); await waiting; return new Response(null, { status: 200 }); }).runOnce();
  try {
    await Promise.race([started, pending.then(() => { throw new Error("Worker finished before its provider effect"); })]); expect(await f.worker().runOnce()).toBe(false);
    await withTransaction(admin, async tx => { await tx.query("SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT", [f.userId]); });
  } finally { release(); await pending; }
  expect((await f.resources())[0].state).toBe("completed");
});
it("deletes Figma webhooks before erasing the connection and disables its related local dependencies", async () => {
  const f = await fixture(), connection = await f.add("figma", ""), spaceId = randomUUID(), domain = randomUUID(), integration = randomUUID(), shared = randomUUID(), binding = randomUUID(), webhook = randomUUID();
  spaces.push(spaceId); domains.push(domain);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, f.userId, spaceId]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Cleanup Space',$3)", [spaceId, f.userId, domain]);
    await tx.query("INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id) VALUES($1,$2,'figma','Figma','private',$3)", [integration, spaceId, f.userId]);
    await tx.query("INSERT INTO provider_shared_resources(id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope) VALUES($1,$2,$3,$4,'figma','file','file-key','Design','drawings')", [shared, spaceId, integration, f.userId]);
    await tx.query("INSERT INTO figma_space_bindings(id,space_id,connection_id,integration_id,shared_resource_id,bound_by_user_id,resource_type,external_id,display_name,file_key) VALUES($1,$2,$3,$4,$5,$6,'file','file-key','Design','file-key')", [binding, spaceId, connection.id, integration, shared, f.userId]);
    await tx.query("INSERT INTO figma_webhook_subscriptions(id,binding_id,webhook_id,event_type,passcode_hash) VALUES($1,$2,'webhook-remote','FILE_UPDATE',$3)", [webhook, binding, "a".repeat(64)]);
  });
  const other = await fixture(), otherIntegration = randomUUID();
  await admin.query("UPDATE account_deletion_requests SET cleanup_owner='go' WHERE id=$1", [other.requestId]);
  await admin.query("INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id) VALUES($1,$2,'google','Other member','other-private',$3)", [otherIntegration, spaceId, other.userId]);
  const send = vi.fn<typeof fetch>(async () => Response.json({ id: "webhook-remote" })), worker = f.worker(send);
  await worker.runOnce(); expect((await f.resources()).find(row => row.kind === "connected").state).toBe("pending");
  expect((await f.resources()).find(row => row.kind === "figma_webhook").state).toBe("completed");
  await worker.runOnce(); await worker.runOnce(); expect((await f.stage()).state).toBe("completed"); expect(send).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT status FROM space_integrations WHERE id=$1", [integration])).rows[0].status).toBe("disabled");
  expect((await admin.query("SELECT disabled_at FROM figma_space_bindings WHERE id=$1", [binding])).rows[0].disabled_at).toBeTruthy();
  expect((await admin.query("SELECT status FROM space_integrations WHERE id=$1", [otherIntegration])).rows[0].status).toBe("active");
});
it("inventories legacy integration subscriptions and keeps their credential until remote cleanup is implemented", async () => {
  const f = await fixture(), spaceId = randomUUID(), domain = randomUUID(), integration = randomUUID(), credential = randomUUID(); spaces.push(spaceId); domains.push(domain);
  const old = JSON.parse(await readFile(new URL("../../../../../../../docs/migration/fixtures/legacy-provider-credentials.json", import.meta.url), "utf8")).find((row: { provider: string }) => row.provider === "google");
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, f.userId, spaceId]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Integration Cleanup',$3)", [spaceId, f.userId, domain]);
    await tx.query("INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id) VALUES($1,$2,'google','Calendar','private',$3)", [integration, spaceId, f.userId]);
    await tx.query("INSERT INTO space_provider_credentials(id,integration_id,space_id,user_id,provider,ciphertext,nonce) VALUES($1,$2,$3,$4,'google',$5,$6)", [credential, integration, spaceId, f.userId, Buffer.from(old.ciphertext, "base64"), Buffer.from(old.nonce, "base64")]);
    await tx.query("INSERT INTO provider_subscriptions(id,integration_id,user_id,provider,resource_key,external_subscription_id,expires_at) VALUES($1,$2,$3,'google','calendar','remote-channel',now()-interval '1 day')", [randomUUID(), integration, f.userId]);
  });
  const send = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 })), worker = f.worker(send);
  await worker.runOnce(); await worker.runOnce();
  expect((await f.resources()).map(row => ({ kind: row.kind, state: row.state }))).toEqual([{ kind: "integration", state: "pending" }, { kind: "provider_subscription", state: "pending" }]);
  expect(send).not.toHaveBeenCalled(); expect((await f.stage()).state).toBe("pending");
  expect((await admin.query("SELECT ciphertext FROM space_provider_credentials WHERE id=$1", [credential])).rows[0].ciphertext).toEqual(Buffer.from(old.ciphertext, "base64"));
  const other = await fixture(); await admin.query("UPDATE account_deletion_requests SET cleanup_owner='go' WHERE id=$1", [other.requestId]);
  await admin.query("UPDATE provider_subscriptions SET user_id=$2 WHERE integration_id=$1", [integration, other.userId]);
  await f.due(); await worker.runOnce();
  expect((await f.stage()).last_error_code).toBe("providers_cleanup_unavailable"); expect(send).not.toHaveBeenCalled();
});
it("rolls back resource acknowledgement and source erasure if its lease expires during the final transaction", async () => {
  const f = await fixture(), connection = await f.add(), jobs = createAccountDeletionJobs(application), repository = createDeletionProviderRepository(application);
  const job = (await jobs.claim("providers"))!, work = (await repository.prepare(job))!;
  await admin.query(`CREATE FUNCTION misty_provider_ack_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    IF NEW.request_id='${f.requestId}' AND NEW.state='completed' THEN PERFORM pg_sleep(0.3); END IF; RETURN NEW; END $$;
    CREATE TRIGGER misty_provider_ack_delay BEFORE UPDATE ON account_deletion_provider_resources FOR EACH ROW EXECUTE FUNCTION misty_provider_ack_delay();`);
  try {
    await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()+interval '0.2 seconds' WHERE request_id=$1 AND step='providers'", [f.requestId]);
    await expect(repository.complete(job, work, "token_revoked")).rejects.toThrow("Provider cleanup lease expired");
    expect((await f.resources())[0]).toMatchObject({ state: "pending", ciphertext: connection.ciphertext, outcome: "" });
    expect((await admin.query("SELECT credential_ciphertext,revoked_at FROM connected_accounts WHERE id=$1", [connection.id])).rows[0]).toEqual({ credential_ciphertext: connection.ciphertext, revoked_at: null });
  } finally { await admin.query("DROP TRIGGER misty_provider_ack_delay ON account_deletion_provider_resources; DROP FUNCTION misty_provider_ack_delay()"); }
});
const dropboxClients = { dropbox: { clientId: "fixture-dropbox-client", clientSecret: "fixture-dropbox-secret" } };
const refreshedDropbox = () => Response.json({ access_token: "derived-access", token_type: "bearer", expires_in: 14400 });
it("checkpoints encrypted Dropbox refresh proof before revoking a token derived from that refresh token", async () => {
  const f = await fixture(), connection = await f.add("dropbox"); let calls = 0;
  await f.worker(async (url, init) => {
    if (String(url).endsWith("/oauth2/token")) { calls++; expect(new URLSearchParams(String(init!.body)).get("refresh_token")).toBe("fixture-refresh"); return refreshedDropbox(); }
    calls++;
    const row = (await admin.query("SELECT * FROM account_deletion_provider_resources WHERE request_id=$1", [f.requestId])).rows[0];
    expect(row.state).toBe("pending"); expect(row.ciphertext).toEqual(connection.ciphertext); expect(row.execution_ciphertext).toBeInstanceOf(Buffer);
    const tokens = JSON.parse(cipher.decrypt("dropbox", row.execution_ciphertext, row.execution_nonce, row.execution_key_version).toString());
    expect(tokens).toEqual({ access_token: "derived-access", refresh_token: "fixture-refresh" });
    expect(new Headers(init!.headers).get("Authorization")).toBe("Bearer derived-access");
    return new Response(null, { status: 200 });
  }, dropboxClients).runOnce();
  expect(calls).toBe(2);
  expect((await admin.query("SELECT state,ciphertext,execution_ciphertext,execution_nonce,execution_key_version FROM account_deletion_provider_resources WHERE request_id=$1", [f.requestId])).rows[0])
    .toEqual({ state: "completed", ciphertext: null, execution_ciphertext: null, execution_nonce: null, execution_key_version: null });
});
it("recovers a lost Dropbox revocation response using checkpointed token lineage and the same OAuth client", async () => {
  const f = await fixture(); await f.add("dropbox"); let revoked = false; const calls: string[] = [];
  const worker = f.worker(async url => {
    if (String(url).endsWith("/oauth2/token")) { calls.push("refresh"); return revoked ? Response.json({ error: "invalid_grant" }, { status: 400 }) : refreshedDropbox(); }
    calls.push("revoke"); if (!revoked) { revoked = true; throw new Error("lost acknowledgement"); } return new Response(null, { status: 401 });
  }, dropboxClients);
  await worker.runOnce(); expect((await f.resources())[0].state).toBe("pending");
  expect((await admin.query("SELECT execution_ciphertext FROM account_deletion_provider_resources WHERE request_id=$1", [f.requestId])).rows[0].execution_ciphertext).toBeInstanceOf(Buffer);
  await f.due(); await worker.runOnce(); expect((await f.resources())[0]).toMatchObject({ state: "completed", outcome: "token_inactive" });
  expect(calls).toEqual(["refresh", "revoke", "revoke", "refresh"]);
});
it("retries a lost reusable-token refresh without certifying a request that never reached revocation", async () => {
  const f = await fixture(), connection = await f.add("dropbox"); let first = true, revocations = 0;
  const worker = f.worker(async url => {
    if (String(url).endsWith("/oauth2/token")) { if (first) { first = false; throw new Error("lost refresh response"); } return refreshedDropbox(); }
    revocations++; return new Response(null, { status: 200 });
  }, dropboxClients);
  await worker.runOnce(); expect((await f.resources())[0]).toMatchObject({ state: "pending", ciphertext: connection.ciphertext }); expect(revocations).toBe(0);
  await f.due(); await worker.runOnce(); expect(revocations).toBe(1); expect((await f.resources())[0].state).toBe("completed");
});
it("does not treat an initial invalid_grant or changed OAuth client as proof of Dropbox revocation", async () => {
  const f = await fixture(); await f.add("dropbox");
  await f.worker(async () => Response.json({ error: "invalid_grant" }, { status: 400 }), dropboxClients).runOnce();
  expect((await f.resources())[0].state).toBe("pending");
  await f.due(); await f.worker(async url => String(url).endsWith("/oauth2/token") ? refreshedDropbox() : new Response(null, { status: 503 }), dropboxClients).runOnce();
  const saved = (await admin.query("SELECT execution_ciphertext FROM account_deletion_provider_resources WHERE request_id=$1", [f.requestId])).rows[0].execution_ciphertext;
  expect(saved).toBeInstanceOf(Buffer); await f.due();
  const send = vi.fn<typeof fetch>(async () => Response.json({ error: "invalid_grant" }, { status: 400 }));
  await f.worker(send, { dropbox: { ...dropboxClients.dropbox, clientId: "another-app" } }).runOnce();
  expect(send).not.toHaveBeenCalled(); expect((await f.resources())[0].state).toBe("pending");
});
it("does not revoke a refreshed Dropbox token after its checkpoint lease has expired", async () => {
  const f = await fixture(); await f.add("dropbox"); const urls: string[] = [];
  await f.worker(async url => {
    urls.push(String(url)); await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE request_id=$1 AND step='providers'", [f.requestId]);
    return refreshedDropbox();
  }, dropboxClients).runOnce();
  expect(urls).toEqual(["https://api.dropboxapi.com/oauth2/token"]); expect((await f.resources())[0].state).toBe("pending");
  expect((await admin.query("SELECT execution_ciphertext FROM account_deletion_provider_resources WHERE request_id=$1", [f.requestId])).rows[0].execution_ciphertext).toBeNull();
});
it("uses a legacy cloud credential's original custom Dropbox client instead of the current default app", async () => {
  const f = await fixture(), id = `cloud_${randomUUID()}`, nonce = Buffer.alloc(12, 27), encrypt = createCipheriv("aes-256-gcm", Buffer.alloc(32, 19), nonce);
  encrypt.setAAD(Buffer.from("misty-provider-v2:dropbox"));
  const plaintext = JSON.stringify({ Token: { access_token: "old-access", refresh_token: "custom-refresh" }, Custom: true, ClientID: "original-app", ClientSecret: "original-secret" });
  const ciphertext = Buffer.concat([encrypt.update(plaintext), encrypt.final(), encrypt.getAuthTag()]);
  await admin.query("INSERT INTO cloud_connections(id,user_id,provider,name,account_id,credential_ciphertext,credential_nonce,uses_custom_oauth_client) VALUES($1,$2,'dropbox','Custom Dropbox','custom-account',$3,$4,true)", [id, f.userId, ciphertext, nonce]);
  await f.worker(async (url, init) => {
    if (String(url).endsWith("/oauth2/token")) {
      const body = new URLSearchParams(String(init!.body)); expect(body.get("client_id")).toBe("original-app"); expect(body.get("client_secret")).toBe("original-secret"); expect(body.get("refresh_token")).toBe("custom-refresh");
      return refreshedDropbox();
    }
    return new Response(null, { status: 200 });
  }, dropboxClients).runOnce();
  expect((await f.resources())[0]).toMatchObject({ kind: "cloud", state: "completed", outcome: "token_revoked" });
});
