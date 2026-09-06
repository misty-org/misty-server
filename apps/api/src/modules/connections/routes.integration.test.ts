import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { parseMethodResult } from "@misty/contracts";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createOfficialCatalog } from "../official-apps/catalog.js";
import { createInstallationRepository } from "../official-apps/repository.js";
import { createSpaceRepository } from "../spaces/repository.js";
import { createConnectionRepository } from "./repository.js";
import { createConnectionCipher } from "./credentials.js";
import { createConnectionRevoker } from "./revocation.js";
import { createConnectionTokenBroker } from "./token-broker.js";
import { TokenExchangeError, type TokenRefresher } from "./oauth-token.js";
import { createConnectionAuthorizationService } from "./oauth/service.js";
import { connectionOAuthCatalog } from "./oauth/catalog.js";
import { createIntegrationRepository } from "./integrations.js";

const admin = createTestDatabase(), users: string[] = [];
it("binds owned Calendar accounts through SDK and REST, re-encrypts credentials and keeps stable references", async () => {
  const f = await fixture(), id = await f.connection(), token = await f.appToken();
  await admin.query("UPDATE connected_accounts SET capabilities='[\"calendar_read\",\"calendar_write\"]',expires_at='2026-10-01T00:00:00Z' WHERE id=$1", [id]);
  const response = await f.rpc("integrations.bind", { path: { provider: "google" }, body: { connection_id: id, capability: "calendar_read" } }, token);
  expect(response.status, await response.clone().text()).toBe(201); const result = await response.json();
  expect(() => parseMethodResult("integrations.bind", result)).not.toThrow();
  expect(result).toMatchObject({ connection_id: id, capability: "calendar_read", integration: { space_id: f.spaceId, provider: "google", display_name: "Account", connected_by_user_id: f.owner.user.id, granted_permissions: ["provider-private-scope"], status: "active" } });
  expect(JSON.stringify(result)).not.toMatch(/credential|test-only-private|test-only-refresh/);
  const credential = (await admin.query("SELECT * FROM space_provider_credentials WHERE integration_id=$1", [result.integration.id])).rows[0];
  expect(credential.expires_at.toISOString()).toBe("2026-10-01T00:00:00.000Z");
  const plain = f.cipher.decryptLegacy("google", credential.ciphertext, credential.nonce, credential.key_version);
  try { expect(JSON.parse(plain.toString())).toEqual({ access_token: "test-only-private-token", refresh_token: "test-only-refresh" }); } finally { plain.fill(0); }
  expect(() => f.cipher.decrypt("google", credential.ciphertext, credential.nonce, credential.key_version)).toThrow();
  for (const prefix of ["", "/api", "/v1"]) {
    const bound = await f.app.request(`${prefix}/spaces/${f.spaceId}/integrations/google/bind`, { method: "POST", headers: { Authorization: `Bearer ${f.owner.token}`, "Content-Type": "application/json" }, body: JSON.stringify({ connection_id: id }) });
    expect(bound.status).toBe(201); expect((await bound.json()).integration.id).toBe(result.integration.id);
    const list = await f.request(`${prefix}/spaces/${f.spaceId}/integrations`); expect(list.status).toBe(200);
    const listed = await list.json(); expect(() => parseMethodResult("integrations.list", listed)).not.toThrow();
    expect(listed.integrations).toHaveLength(1); expect(listed.providers).toEqual([{ provider: "github", configured: false }]);
  }
  const listed = await f.rpc("integrations.list", {}, token); expect(listed.status).toBe(200); const listResult = await listed.json();
  expect(() => parseMethodResult("integrations.list", listResult)).not.toThrow(); expect(listResult.integrations).toHaveLength(1);
  const final = (await admin.query("SELECT i.credential_reference,c.id FROM space_integrations i JOIN space_provider_credentials c ON c.integration_id=i.id WHERE i.id=$1", [result.integration.id])).rows[0];
  expect(final).toEqual({ credential_reference: credential.id, id: credential.id }); expect(f.provider).not.toHaveBeenCalled();
});

it("requires current Space integration permission, account ownership, capability and App scope", async () => {
  const f = await fixture(), id = await f.connection(), foreign = await f.connection("google", f.other.user.id);
  const bind = (connectionId: string, token = f.owner.token, provider = "google", capability = "calendar_read") => f.app.request(`/spaces/${f.spaceId}/integrations/${provider}/bind`, {
    method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: JSON.stringify({ connection_id: connectionId, capability }) });
  expect((await bind(id)).status).toBe(403);
  await admin.query("UPDATE connected_accounts SET capabilities='[\"calendar_read\"]' WHERE id=ANY($1::text[])", [[id, foreign]]);
  expect((await bind(foreign)).status).toBe(404); expect((await bind(id, f.owner.token, "microsoft")).status).toBe(403);
  expect((await bind(id, f.owner.token, "google", "calendar_write")).status).toBe(403);
  expect((await bind(id, await f.appToken(["connections.read"]))).status).toBe(403);
  expect((await f.request(`/spaces/${f.spaceId}/integrations`, "GET", f.other.token)).status).toBe(403);
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [f.spaceId, f.other.user.id]);
  expect((await f.request(`/spaces/${f.spaceId}/integrations`, "GET", f.other.token)).status).toBe(200);
  expect((await bind(foreign, f.other.token)).status).toBe(403);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'integrations.manage','allow',$3)", [f.spaceId, f.other.user.id, f.owner.user.id]);
  expect((await bind(foreign, f.other.token)).status).toBe(201);
  await admin.query("UPDATE connected_accounts SET revoked_at=now(),status='revoked' WHERE id=$1", [id]); expect((await bind(id)).status).toBe(404);
  expect(f.provider).not.toHaveBeenCalled();
});

it("rolls back integration creation when credential persistence fails", async () => {
  const f = await fixture(), id = await f.connection(), token = await f.appToken();
  await admin.query("UPDATE connected_accounts SET capabilities='[\"calendar_read\"]' WHERE id=$1", [id]);
  await admin.query("REVOKE INSERT ON space_provider_credentials FROM misty_hono_app_test");
  try { expect((await f.rpc("integrations.bind", { path: { provider: "google" }, body: { connection_id: id, capability: "calendar_read" } }, token)).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_provider_credentials TO misty_hono_app_test"); }
  expect((await admin.query("SELECT id FROM space_integrations WHERE space_id=$1", [f.spaceId])).rowCount).toBe(0);
  expect((await f.state(id)).status).toBe("active"); expect(f.provider).not.toHaveBeenCalled();
});
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,owner_storage_usage,
      space_setup_integrations,space_creation_requests,space_events,user_app_installations,app_runtime_sessions,app_install_events,app_data_deletion_jobs TO misty_hono_app_test;
    GRANT SELECT ON space_invitations,space_member_permission_overrides TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON space_integrations,space_provider_credentials TO misty_hono_app_test;
    GRANT SELECT,UPDATE,INSERT ON connected_accounts TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON connection_authorization_requests TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON self_host_accounts TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON figma_webhook_subscriptions,figma_space_bindings,provider_shared_resources,space_integrations TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map((row) => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const runtime = createAppRuntimeRepository(application), installations = createInstallationRepository(application);
  const cipher = createConnectionCipher(Buffer.alloc(32, 11));
  const provider = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 }));
  const repository = createConnectionRepository(application, createConnectionRevoker(cipher, provider));
  const authorization = createConnectionAuthorizationService({ pool: application, cipher, deployment: "hosted", fetcher: provider,
    clients: Object.fromEntries(["google", "microsoft", "dropbox", "figma", "discord", "instagram"].map((name) => [name, { clientId: `${name}-client`, clientSecret: `${name}-secret` }])), config: { apiBase: "https://api.example.invalid/v1" } });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }), deployment: "hosted" },
    connections: { auth, appRuntime: runtime, repository, authorization, providers: { google: true, microsoft: false },
      integrations: createIntegrationRepository(application, cipher), integrationProviders: [{ provider: "github", configured: false }] }, appRuntime: { repository: runtime } });
  const account = async () => {
    const username = `conn_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username, email: `${username}@example.invalid`, name: "Connection test", password: "test-password", analyticsEnabled: false }); users.push(result.user.id); return result;
  };
  const owner = await account(), other = await account();
  const { space } = await createSpaceRepository(application).create(owner.user.id, { name: "Connections", template_id: "blank", integration_providers: [] }, "");
  const request = (path: string, method = "GET", token = owner.token) => app.request(path, { method, headers: { Authorization: `Bearer ${token}` } });
  const rpc = (method: string, params: unknown, token: string) => app.request("/v1/app-runtime/rpc", { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: JSON.stringify({ protocol: 2, method, params }) });
  const connection = async (providerName = "google", userId = owner.user.id) => {
    const id = `connection_${randomUUID()}`, encrypted = cipher.encrypt(providerName, Buffer.from(JSON.stringify({ access_token: "test-only-private-token", refresh_token: "test-only-refresh" })));
    await admin.query(`INSERT INTO connected_accounts(id,user_id,provider,account_id,account_display,credential_ciphertext,credential_nonce,capabilities,granted_scopes)
      VALUES($1,$2,$3,$1,'Account',$4,$5,'["mail"]','["provider-private-scope"]')`, [id, userId, providerName, encrypted.ciphertext, encrypted.nonce]); return id;
  };
  const appToken = async (scopes = ["connections.read", "connections.write"]) => {
    await installations.install(owner.user.id, { ...createOfficialCatalog().find("terminal")!, scopes });
    const token = randomUUID(); await installations.session(owner.user.id, "terminal", hashToken(token), space.id); return token;
  };
  const state = async (id: string) => (await admin.query("SELECT status,revoked_at,octet_length(credential_ciphertext) AS bytes,octet_length(credential_nonce) AS nonce_bytes FROM connected_accounts WHERE id=$1", [id])).rows[0];
  const begin = async (name = "google", token = owner.token, body: unknown = { capabilities: ["mail"], return_to: "/settings" }, prefix = "/v1") => app.request(`${prefix}/connections/${name}/authorize`, {
    method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", Host: "attacker.invalid", "X-Forwarded-Host": "attacker.invalid" }, body: JSON.stringify(body) });
  const callback = (url: string, extra = "code=fixture-code", name = new URL(url).pathname.includes("microsoft") ? "microsoft" : "google") => app.request(`/v1/oauth/connections/${name}/callback?state=${new URL(url).searchParams.get("state")}&${extra}`);
  return { app, owner, other, repository, installations, runtime, cipher, spaceId: space.id, request, rpc, connection, appToken, state, provider, authorization, begin, callback };
}

it("completes all six native OAuth providers with encrypted state, exact callbacks and private credentials", async () => {
  const f = await fixture();
  for (const providerName of ["google", "microsoft", "dropbox", "figma", "discord", "instagram"] as const) {
    const started = await f.begin(providerName, f.owner.token, {}); expect(started.status).toBe(200);
    const body = await started.json(); expect(() => parseMethodResult("connections.authorize", body)).not.toThrow();
    const url = new URL(body.authorization_url), stateHash = hashToken(url.searchParams.get("state")!);
    expect(url.searchParams.get("redirect_uri")).toBe(`https://api.example.invalid/v1/oauth/connections/${providerName}/callback`);
    expect(body.authorization_url).not.toContain("attacker.invalid"); expect(started.headers.get("Cache-Control")).toBe("no-store");
    const saved = (await admin.query("SELECT * FROM connection_authorization_requests WHERE state_hash=$1", [stateHash])).rows[0];
    expect(saved.actor.userId).toBe(f.owner.user.id); expect(saved.actor.sessionHash).toBe(hashToken(f.owner.token));
    const verifier = f.cipher.decrypt(providerName, saved.verifier_ciphertext, saved.verifier_nonce, 1).toString(); expect(verifier).toMatch(/^[\w-]{64}$/);
    const accountId = `${providerName}-identity`;
    f.provider.mockImplementation(async (input, init) => {
      const target = new URL(String(input));
      if (target.href === connectionOAuthCatalog[providerName].identity || target.hostname === "graph.instagram.com") {
        return Response.json({ [providerName === "google" ? "sub" : providerName === "dropbox" ? "account_id" : "id"]: accountId, email: "<script>alert(1)</script>" });
      }
      const form = new URLSearchParams(String(init!.body)); expect(form.get("redirect_uri")).toBe(url.searchParams.get("redirect_uri"));
      if (providerName !== "instagram") expect(form.get("code_verifier")).toBe(verifier);
      return Response.json({ access_token: `${providerName}-private-access`, refresh_token: "private-refresh", scope: saved.requested_scopes.join(" "), expires_in: 3600 });
    });
    const response = await f.callback(body.authorization_url, "code=fixture-code", providerName); expect(response.status).toBe(200);
    const page = await response.text(); expect(page).toContain("&lt;script&gt;"); expect(page).not.toMatch(/private-access|private-refresh|fixture-code|<script>/);
    expect(response.headers.get("Referrer-Policy")).toBe("no-referrer"); expect(response.headers.get("Content-Security-Policy")).toContain("default-src 'none'");
    const account = (await admin.query("SELECT * FROM connected_accounts WHERE user_id=$1 AND provider=$2", [f.owner.user.id, providerName])).rows[0];
    expect(account.account_id).toBe(accountId); expect(account.status).toBe("active"); expect(account.capabilities).toEqual(body.capabilities);
    expect(JSON.parse(f.cipher.decrypt(providerName, account.credential_ciphertext, account.credential_nonce, account.key_version).toString()).access_token).toBe(`${providerName}-private-access`);
    const calls = f.provider.mock.calls.length;
    expect((await f.callback(body.authorization_url, "code=fixture-code", providerName)).status).toBe(400); expect(f.provider).toHaveBeenCalledTimes(calls);
    expect((await admin.query("SELECT octet_length(verifier_ciphertext) AS bytes FROM connection_authorization_requests WHERE state_hash=$1", [stateHash])).rows[0].bytes).toBe(0);
  }
});

it("authorizes through every REST alias and native RPC with current App grants and bounded input", async () => {
  const f = await fixture(), token = await f.appToken(), params = { path: { spaceID: f.spaceId, provider: "google" }, body: { capabilities: ["mail"], return_to: "/settings" } };
  for (const prefix of ["", "/api", "/v1"]) expect((await f.begin("google", f.owner.token, params.body, prefix)).status).toBe(200);
  const response = await f.rpc("connections.authorize", params, token); expect(response.status).toBe(200);
  const result = await response.json(); expect(() => parseMethodResult("connections.authorize", result)).not.toThrow();
  const state = (await admin.query("SELECT actor,expires_at FROM connection_authorization_requests WHERE state_hash=$1", [hashToken(new URL(result.authorization_url).searchParams.get("state")!)])).rows[0];
  expect(state.actor.appSession.token_hash).toBe(hashToken(token)); expect(state.expires_at.getTime()).toBeLessThanOrEqual(Date.parse(state.actor.appSession.expires_at));
  expect((await f.begin("google", await f.appToken(["connections.read"]), params.body)).status).toBe(403);
  for (const body of [{ capabilities: ["unknown"] }, { return_to: "//evil.invalid" }, { return_to: "/settings", endpoint: "https://evil.invalid" }]) expect((await f.begin("google", f.owner.token, body)).status).toBe(400);
  expect((await f.begin("unknown")).status).toBe(400);
  expect((await f.begin("google", "unauthenticated", { capabilities: ["x".repeat(20000)] })).status).toBe(401);
  expect((await f.begin("google", f.owner.token, { return_to: "x".repeat(20000) })).status).toBe(400);
  expect(f.provider).not.toHaveBeenCalled();
});

it("rejects provider/state confusion, malformed callbacks, expired states and replayed refusals", async () => {
  const f = await fixture(), begin = await (await f.begin()).json(), url = begin.authorization_url;
  expect((await f.callback(url, "code=fixture-code", "microsoft")).status).toBe(400);
  const stateHash = hashToken(new URL(url).searchParams.get("state")!);
  expect((await admin.query("SELECT consumed_at FROM connection_authorization_requests WHERE state_hash=$1", [stateHash])).rows[0].consumed_at).toBeNull();
  for (const extra of ["code=one&code=two", "code=one&error=denied", "", "state=duplicate&code=one"]) expect((await f.callback(url, extra)).status).toBe(400);
  expect((await f.callback(url, "error=access_denied&error_description=private-description")).status).toBe(400);
  expect((await f.callback(url)).status).toBe(400);
  const expired = await (await f.begin()).json(); await admin.query("UPDATE connection_authorization_requests SET expires_at=now()-interval '1 second' WHERE user_id=$1", [f.owner.user.id]);
  expect((await f.callback(expired.authorization_url)).status).toBe(400); expect(f.provider).not.toHaveBeenCalled();
});

it("rechecks the initiating account session and App installation before exchanging or saving", async () => {
  const f = await fixture(), token = await f.appToken(), appFlow = await (await f.begin("google", token)).json();
  await f.installations.uninstall(f.owner.user.id, "terminal");
  expect((await f.callback(appFlow.authorization_url)).status).toBe(400); expect(f.provider).not.toHaveBeenCalled();
  const direct = await (await f.begin()).json();
  await admin.query("DELETE FROM sessions WHERE token_hash=$1", [hashToken(f.owner.token)]);
  expect((await f.callback(direct.authorization_url)).status).toBe(400); expect(f.provider).not.toHaveBeenCalled();
});

it("allows only one callback to exchange a code and removes declined permissions during reconnection", async () => {
  const f = await fixture(), id = await f.connection();
  await admin.query("UPDATE connected_accounts SET capabilities='[\"mail\",\"files\"]',granted_scopes='[\"https://www.googleapis.com/auth/drive\"]' WHERE id=$1", [id]);
  const flow = await (await f.begin("google", f.owner.token, { capabilities: ["mail", "files"] })).json();
  const entered = deferred(), release = deferred();
  f.provider.mockImplementation(async (url) => {
    if (String(url).includes("/token")) { entered.resolve(); await release.promise; return Response.json({ access_token: "new-private", scope: "https://www.googleapis.com/auth/drive", expires_in: 3600 }); }
    return Response.json({ sub: id, email: "new@example.invalid" });
  });
  const first = f.callback(flow.authorization_url); await entered.promise;
  try { expect((await f.callback(flow.authorization_url)).status).toBe(400); expect(f.provider).toHaveBeenCalledTimes(1); } finally { release.resolve(); }
  expect((await first).status).toBe(200);
  const stored = (await admin.query("SELECT * FROM connected_accounts WHERE id=$1", [id])).rows[0];
  expect(stored.capabilities).toEqual(["files"]); expect(stored.granted_scopes).toEqual(["https://www.googleapis.com/auth/drive"]);
  expect(JSON.parse(f.cipher.decrypt("google", stored.credential_ciphertext, stored.credential_nonce, stored.key_version).toString()).refresh_token).toBe("test-only-refresh");
});

it("does not resurrect removed accounts or overwrite refreshes that finish during consent", async () => {
  const f = await fixture(), id = await f.connection();
  const flow = await (await f.begin()).json();
  const entered = deferred(), release = deferred();
  f.provider.mockImplementation(async (url) => {
    if (String(url).includes("/token")) { entered.resolve(); await release.promise; return Response.json({ access_token: "stale-new-token" }); }
    if (String(url).includes("/userinfo")) return Response.json({ sub: id });
    return new Response(null, { status: 200 });
  });
  const callback = f.callback(flow.authorization_url); await entered.promise;
  try { expect((await f.request(`/connections/${id}`, "DELETE")).status).toBe(204); } finally { release.resolve(); }
  expect((await callback).status).toBe(400); expect((await f.state(id)).status).toBe("revoked"); expect((await f.state(id)).bytes).toBe(0);
  // An explicit new flow after removal can reconnect, but a later credential rotation wins.
  const reconnect = await (await f.begin()).json(), encrypted = f.cipher.encrypt("google", Buffer.from('{"access_token":"concurrent-rotation"}'));
  await admin.query("UPDATE connected_accounts SET credential_ciphertext=$2,credential_nonce=$3,status='active',revoked_at=NULL WHERE id=$1", [id, encrypted.ciphertext, encrypted.nonce]);
  expect((await f.callback(reconnect.authorization_url)).status).toBe(400);
  expect((await admin.query("SELECT credential_nonce FROM connected_accounts WHERE id=$1", [id])).rows[0].credential_nonce).toEqual(encrypted.nonce);
});

it("blocks a callback when its App is revoked during provider exchange and never exposes failure details", async () => {
  const f = await fixture(), token = await f.appToken(), flow = await (await f.begin("google", token)).json();
  f.provider.mockImplementation(async (url) => {
    if (String(url).includes("/token")) { await f.installations.uninstall(f.owner.user.id, "terminal"); return Response.json({ access_token: "private-provider-token" }); }
    return Response.json({ sub: "never-saved", email: "private@example.invalid" });
  });
  const response = await f.callback(flow.authorization_url); expect(response.status).toBe(400); expect(await response.text()).not.toMatch(/private|SQL|app_session/);
  expect((await admin.query("SELECT id FROM connected_accounts WHERE user_id=$1", [f.owner.user.id])).rowCount).toBe(0);
});

it("preserves Figma incremental consent and bounds authorization issuance across aliases", async () => {
  const f = await fixture(), id = await f.connection("figma");
  await admin.query("UPDATE connected_accounts SET capabilities='[\"drawings_read\"]' WHERE id=$1", [id]);
  const first = await (await f.begin("figma", f.owner.token, { capabilities: ["drawings_comments"] })).json();
  expect(first.capabilities).toEqual(["drawings_comments", "drawings_read"]);
  expect(new URL(first.authorization_url).searchParams.get("scope")).toContain("file_content:read");
  for (let index = 1; index < 20; index++) expect((await f.begin("google", f.owner.token, {}, index % 2 ? "/api" : "")).status).toBe(200);
  expect((await f.begin()).status).toBe(429);
  expect((await admin.query("SELECT 1 FROM connection_authorization_requests WHERE user_id=$1", [f.owner.user.id])).rowCount).toBe(20);
});

it("binds callbacks to the configured client and owner, with RLS protecting authorization state", async () => {
  const f = await fixture(), flow = await (await f.begin()).json();
  const otherService = createConnectionAuthorizationService({ pool: application, cipher: f.cipher, deployment: "hosted", fetcher: f.provider,
    clients: { google: { clientId: "changed-client", clientSecret: "secret" } }, config: { apiBase: "https://api.example.invalid/v1" } });
  const query = new URLSearchParams({ state: new URL(flow.authorization_url).searchParams.get("state")!, code: "fixture" });
  await expect(otherService.callback("google", query, new AbortController().signal)).rejects.toThrow("authorization_expired");
  expect(f.provider).not.toHaveBeenCalled();
  expect(await withTransaction(application, async (tx) => (await tx.query("SELECT 1 FROM connection_authorization_requests WHERE user_id=$1", [f.owner.user.id])).rowCount, { mode: "user", userId: f.other.user.id })).toBe(0);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.owner.user.id]);
  expect((await f.callback(flow.authorization_url)).status).toBe(400); expect(f.provider).not.toHaveBeenCalled();
});

it("rechecks self-host entitlement for callbacks arriving without desktop cookies", async () => {
  const f = await fixture(), service = createConnectionAuthorizationService({ pool: application, cipher: f.cipher, deployment: "self_hosted", fetcher: f.provider,
    clients: { google: { clientId: "google-client", clientSecret: "google-secret" } }, config: { apiBase: "https://api.example.invalid/v1" } });
  const actor = { userId: f.owner.user.id, sessionHash: hashToken(f.owner.token) };
  await expect(service.begin(actor, "google", {})).rejects.toThrow("authorization_expired");
  await admin.query("INSERT INTO self_host_accounts(user_id,entitlement_subject,entitlement_expires_at) VALUES($1,$2,now()+interval '1 day')", [f.owner.user.id, `oauth-fixture-${randomUUID()}`]);
  const flow = await service.begin(actor, "google", {});
  await admin.query("UPDATE self_host_accounts SET disabled_at=now() WHERE user_id=$1", [f.owner.user.id]);
  await expect(service.callback("google", new URLSearchParams({ state: new URL(flow.authorization_url).searchParams.get("state")!, code: "fixture" }), new AbortController().signal)).rejects.toThrow("authorization_expired");
  expect(f.provider).not.toHaveBeenCalled();
});

it("returns only the owner's public metadata through all aliases and native RPC", async () => {
  const f = await fixture(), own = await f.connection(), second = await f.connection("microsoft"), foreign = await f.connection("google", f.other.user.id), revoked = await f.connection();
  await admin.query("UPDATE connected_accounts SET status='revoked',revoked_at=now() WHERE id=$1", [revoked]);
  await admin.query("UPDATE connected_accounts SET status='needs_attention',last_error_code='refresh_failed',expires_at='2026-09-01T00:00:00Z' WHERE id=$1", [second]);
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.request(`${prefix}/connections`); expect(response.status).toBe(200); expect(response.headers.get("Cache-Control")).toBe("no-store");
    const result = await response.json(); expect(() => parseMethodResult("connections.list", result)).not.toThrow();
    expect(result.connections.map((row: { id: string }) => row.id)).toEqual([own, second]);
    expect(result.connections[0]).not.toHaveProperty("expires_at"); expect(result.connections[1].last_error_code).toBe("refresh_failed");
    expect(JSON.stringify(result)).not.toMatch(/credential|nonce|key_version|test-only|user_id/);
    expect(result.providers).toEqual({ google: true, microsoft: false });
  }
  const token = await f.appToken(); expect((await (await f.rpc("connections.list", {}, token)).json()).connections).toHaveLength(2);
  expect((await (await f.request("/connections", "GET", f.other.token)).json()).connections.map((row: { id: string }) => row.id)).toEqual([foreign]);
  expect(f.provider).not.toHaveBeenCalled();
});

it("brokers tokens only for the current owner, provider capability and exact App permission", async () => {
  const f = await fixture(), id = await f.connection(), foreign = await f.connection("google", f.other.user.id);
  const refresh = vi.fn<TokenRefresher>(), broker = createConnectionTokenBroker({ pool: application, cipher: f.cipher, refresh }), signal = new AbortController().signal;
  const token = await f.appToken(["mail.read"]), session = (await f.runtime.findSession(hashToken(token)))!;
  const actor = { userId: f.owner.user.id, appSession: session };
  expect((await broker.acquire(actor, id, "mailRead", signal)).accessToken).toBe("test-only-private-token");
  await expect(broker.acquire(actor, id, "mailWrite", signal)).rejects.toThrow();
  await expect(broker.acquire(actor, foreign, "mailRead", signal)).rejects.toMatchObject({ code: "not_found" });
  await expect(broker.acquire(actor, "../escape", "mailRead", signal)).rejects.toMatchObject({ code: "invalid_request" });
  const unsupported = await f.connection("figma");
  await expect(broker.acquire(actor, unsupported, "mailRead", signal)).rejects.toMatchObject({ code: "provider_unsupported" });
  await admin.query("UPDATE connected_accounts SET capabilities='[]' WHERE id=$1", [id]);
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "capability_required" });
  await admin.query("UPDATE connected_accounts SET capabilities='[\"mail\"]' WHERE id=$1", [id]);
  await f.installations.uninstall(f.owner.user.id, "terminal");
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toThrow();
  expect(refresh).not.toHaveBeenCalled();
});

it("serializes concurrent refreshes, persists rotation, and fences stale provider health reports", async () => {
  const f = await fixture(), id = await f.connection("microsoft"), actor = { userId: f.owner.user.id }, signal = new AbortController().signal;
  const entered = deferred(), release = deferred();
  const refresh = vi.fn<TokenRefresher>(async (_provider, previous) => { expect(previous).toBe("test-only-refresh"); entered.resolve(); await release.promise; return { access_token: "rotated-access", refresh_token: "rotated-refresh", expires_in: 3600 }; });
  const broker = createConnectionTokenBroker({ pool: application, cipher: f.cipher, refresh });
  const old = await broker.acquire(actor, id, "mailRead", signal);
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  const requests = Array.from({ length: 12 }, () => broker.acquire(actor, id, "mailRead", signal));
  await entered.promise; expect(refresh).toHaveBeenCalledTimes(1); release.resolve();
  const leases = await Promise.all(requests); expect(leases.every((lease) => lease.accessToken === "rotated-access")).toBe(true);
  expect(refresh).toHaveBeenCalledTimes(1);
  const stored = (await admin.query("SELECT credential_ciphertext,credential_nonce,key_version,expires_at FROM connected_accounts WHERE id=$1", [id])).rows[0];
  expect(JSON.parse(f.cipher.decrypt("microsoft", stored.credential_ciphertext, stored.credential_nonce, stored.key_version).toString()).refresh_token).toBe("rotated-refresh");
  expect(stored.expires_at.getTime()).toBeGreaterThan(Date.now() + 3500 * 1000);
  expect(await broker.reportAuthorizationFailure(actor, old)).toBe(false); expect((await f.state(id)).status).toBe("active");
  expect(await broker.reportAuthorizationFailure(actor, leases[0]!)).toBe(true);
  expect((await admin.query("SELECT last_error_code FROM connected_accounts WHERE id=$1", [id])).rows[0].last_error_code).toBe("mail_provider_authorization_failed");
  expect((await f.request(`/connections/${id}`, "DELETE")).status).toBe(204);
  expect(await broker.reportAuthorizationFailure(actor, leases[0]!)).toBe(false);
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "not_found" });
  expect((await f.state(id)).bytes).toBe(0);
});

it("commits refresh-failure health, backs off transient errors and requires consent after invalid_grant", async () => {
  const f = await fixture(), id = await f.connection(), actor = { userId: f.owner.user.id }, signal = new AbortController().signal;
  const refresh = vi.fn<TokenRefresher>(async () => { throw new Error("provider-secret-must-not-escape"); });
  const broker = createConnectionTokenBroker({ pool: application, cipher: f.cipher, refresh });
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  for (let n = 0; n < 3; n++) await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ message: "refresh_failed", code: "refresh_failed" });
  expect(refresh).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT status,last_error_code FROM connected_accounts WHERE id=$1", [id])).rows[0]).toEqual({ status: "needs_attention", last_error_code: "refresh_failed" });
  await admin.query("UPDATE connected_accounts SET updated_at=now()-interval '31 seconds' WHERE id=$1", [id]);
  refresh.mockRejectedValue(new TokenExchangeError("reauthorization_required"));
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "reauthorization_required" });
  await admin.query("UPDATE connected_accounts SET updated_at=now()-interval '1 day' WHERE id=$1", [id]);
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "reauthorization_required" });
  expect(refresh).toHaveBeenCalledTimes(2); expect((await f.state(id)).bytes).toBeGreaterThan(0);
});

it("preserves omitted refresh tokens and distinguishes corrupt credentials, missing consent and server configuration", async () => {
  const f = await fixture(), id = await f.connection(), actor = { userId: f.owner.user.id }, signal = new AbortController().signal;
  const refresh = vi.fn<TokenRefresher>(async () => ({ access_token: "fresh-access", expires_in: 3600 }));
  const broker = createConnectionTokenBroker({ pool: application, cipher: f.cipher, refresh });
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  expect((await broker.acquire(actor, id, "mailRead", signal)).accessToken).toBe("fresh-access");
  const row = (await admin.query("SELECT credential_ciphertext,credential_nonce,key_version FROM connected_accounts WHERE id=$1", [id])).rows[0];
  expect(JSON.parse(f.cipher.decrypt("google", row.credential_ciphertext, row.credential_nonce, row.key_version).toString()).refresh_token).toBe("test-only-refresh");
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  refresh.mockRejectedValue(new TokenExchangeError("not_configured"));
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "not_configured" }); expect((await f.state(id)).status).toBe("active");
  const noRefresh = f.cipher.encrypt("google", Buffer.from('{"access_token":"old"}'));
  await admin.query("UPDATE connected_accounts SET credential_ciphertext=$2,credential_nonce=$3 WHERE id=$1", [id, noRefresh.ciphertext, noRefresh.nonce]);
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "reauthorization_required" });
  await admin.query("UPDATE connected_accounts SET credential_ciphertext='broken'::bytea,status='active',last_error_code='' WHERE id=$1", [id]);
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "credential_invalid" });
  expect((await admin.query("SELECT last_error_code FROM connected_accounts WHERE id=$1", [id])).rows[0].last_error_code).toBe("credential_invalid");
});

it("saves a received replacement even when its caller cancels, without returning the private lease", async () => {
  const f = await fixture(), id = await f.connection(), actor = { userId: f.owner.user.id }, controller = new AbortController();
  const refresh = vi.fn<TokenRefresher>(async () => { controller.abort(new Error("Canceled")); return { access_token: "received-before-cancel", refresh_token: "saved-rotation", expires_in: 3600 }; });
  const broker = createConnectionTokenBroker({ pool: application, cipher: f.cipher, refresh });
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  await expect(broker.acquire(actor, id, "mailRead", controller.signal)).rejects.toThrow("Canceled");
  const next = await broker.acquire(actor, id, "mailRead", new AbortController().signal);
  expect(next.accessToken).toBe("received-before-cancel"); expect(refresh).toHaveBeenCalledTimes(1);
});

it("rejects missing refresh expiry, reuses short-lived tokens and never returns an unpersisted replacement", async () => {
  const f = await fixture(), id = await f.connection(), actor = { userId: f.owner.user.id }, signal = new AbortController().signal;
  const refresh = vi.fn<TokenRefresher>(async () => ({ access_token: "missing-expiry" }));
  const broker = createConnectionTokenBroker({ pool: application, cipher: f.cipher, refresh });
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "refresh_failed" });
  await admin.query("UPDATE connected_accounts SET status='active',last_error_code='' WHERE id=$1", [id]);
  refresh.mockResolvedValue({ access_token: "short-lived", expires_in: 120 });
  expect((await broker.acquire(actor, id, "mailRead", signal)).accessToken).toBe("short-lived");
  expect((await broker.acquire(actor, id, "mailRead", signal)).accessToken).toBe("short-lived"); expect(refresh).toHaveBeenCalledTimes(2);
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [id]);
  refresh.mockResolvedValue({ access_token: "must-not-return", refresh_token: "unpersisted-rotation", expires_in: 3600 });
  await admin.query("REVOKE UPDATE ON connected_accounts FROM misty_hono_app_test; GRANT UPDATE(last_error_code) ON connected_accounts TO misty_hono_app_test");
  try {
    await expect(broker.acquire(actor, id, "mailRead", signal)).rejects.toMatchObject({ code: "42501" });
    const row = (await admin.query("SELECT credential_ciphertext,credential_nonce,key_version FROM connected_accounts WHERE id=$1", [id])).rows[0];
    expect(JSON.parse(f.cipher.decrypt("google", row.credential_ciphertext, row.credential_nonce, row.key_version).toString()).access_token).toBe("short-lived");
  } finally { await admin.query("REVOKE UPDATE(last_error_code) ON connected_accounts FROM misty_hono_app_test; GRANT UPDATE ON connected_accounts TO misty_hono_app_test"); }
});

it("requires current Space membership, exact scopes and ownership before removing and erasing credentials", async () => {
  const f = await fixture(), own = await f.connection(), foreign = await f.connection("google", f.other.user.id);
  const reader = await f.appToken(["connections.read"]);
  expect((await f.rpc("connections.remove", { path: { connectionID: own } }, reader)).status).toBe(403);
  expect((await f.request(`/connections/${own}`, "DELETE", reader)).status).toBe(403);
  const token = await f.appToken();
  expect((await f.rpc("connections.remove", { path: { connectionID: foreign } }, token)).status).toBe(404);
  expect((await f.rpc("connections.remove", { path: { connectionID: "../escape" } }, token)).status).toBe(400);
  expect((await f.rpc("connections.remove", { path: { connectionID: own, spaceID: "another" } }, token)).status).toBe(400);
  expect(f.provider).not.toHaveBeenCalled();
  const response = await f.rpc("connections.remove", { path: { connectionID: own } }, token);
  expect(response.status, await response.clone().text()).toBe(204); expect(await response.text()).toBe("");
  expect(() => parseMethodResult("connections.remove", undefined)).not.toThrow();
  expect(await f.state(own)).toMatchObject({ status: "revoked", bytes: 0, nonce_bytes: 0 });
  expect((await f.state(foreign)).status).toBe("active"); expect(f.provider).toHaveBeenCalledTimes(1);
  expect((await f.request(`/connections/${own}`, "DELETE")).status).toBe(404);
  await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [f.spaceId, f.owner.user.id]);
  expect((await f.request("/connections", "GET", token)).status).toBe(403);
  expect((await f.rpc("connections.list", {}, token)).status).toBe(403);
  // The account credential remains valid independently of a particular Space.
  expect((await f.request("/connections")).status).toBe(200);
});

it("erases local credentials when the provider fails and rejects stale app sessions", async () => {
  const f = await fixture(), own = await f.connection(); f.provider.mockRejectedValue(new Error("test-only-provider-token must not escape"));
  const response = await f.request(`/api/connections/${own}`, "DELETE"); expect(response.status).toBe(204);
  expect(response.headers.get("X-Misty-Provider-Revocation")).toBe("provider_revocation_failed_local_credentials_erased");
  expect(await response.text()).toBe(""); expect(await f.state(own)).toMatchObject({ status: "revoked", bytes: 0 });
  const token = await f.appToken(), session = await f.runtime.findSession(hashToken(token));
  await f.installations.uninstall(f.owner.user.id, "terminal");
  expect((await f.request("/connections", "GET", token)).status).toBe(401);
  await expect(f.repository.list({ userId: f.owner.user.id, appSession: session! })).rejects.toThrow();
});

it("does not revoke externally if local SQL cannot commit its planned erasure", async () => {
  const f = await fixture(), own = await f.connection();
  await admin.query("REVOKE UPDATE ON connected_accounts FROM misty_hono_app_test");
  try {
    expect((await f.request(`/connections/${own}`, "DELETE")).status).toBe(500);
    expect((await f.state(own)).status).toBe("active"); expect(f.provider).not.toHaveBeenCalled();
  } finally { await admin.query("GRANT UPDATE ON connected_accounts TO misty_hono_app_test"); }
  const unavailable = createConnectionRepository(application, null);
  await expect(unavailable.remove({ userId: f.owner.user.id }, own, new AbortController().signal)).rejects.toThrow();
  expect((await f.state(own)).status).toBe("active");
});

it("serializes removal with credential replacement and invokes the provider once", async () => {
  const f = await fixture(), own = await f.connection();
  const entered = deferred(), release = deferred();
  f.provider.mockImplementation(async () => { entered.resolve(); await release.promise; return new Response(null, { status: 200 }); });
  const removal = f.request(`/connections/${own}`, "DELETE"); await entered.promise;
  // A real second transaction cannot replace the credential while revocation is
  // in flight; NOWAIT proves the lock without a timing-dependent sleep.
  const other = await admin.connect();
  try {
    await other.query("BEGIN");
    await expect(other.query("SELECT id FROM connected_accounts WHERE id=$1 FOR UPDATE NOWAIT", [own])).rejects.toMatchObject({ code: "55P03" });
    await other.query("ROLLBACK");
  } finally { other.release(); release.resolve(); }
  expect((await removal).status).toBe(204); expect(f.provider).toHaveBeenCalledTimes(1);
  expect(await f.state(own)).toMatchObject({ status: "revoked", bytes: 0 });
});

it("atomically disables the removed Figma connection's hooks, resources and bindings", async () => {
  const f = await fixture(), own = await f.connection("figma"), foreign = await f.connection("figma", f.other.user.id);
  const binding = async (connectionId: string, userId: string) => {
    const id = randomUUID();
    await admin.query(`INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id)
      VALUES($1,$2,'figma',$1,$3,$4)`, [id, f.spaceId, connectionId, userId]);
    await admin.query(`INSERT INTO provider_shared_resources(id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope)
      VALUES($1,$2,$1,$3,'figma','file',$1,'File','read')`, [id, f.spaceId, userId]);
    await admin.query(`INSERT INTO figma_space_bindings(id,space_id,connection_id,integration_id,shared_resource_id,bound_by_user_id,resource_type,external_id,display_name,file_key,status)
      VALUES($1,$2,$3,$1,$1,$4,'file',$1,'File',$1,'active')`, [id, f.spaceId, connectionId, userId]);
    await admin.query("INSERT INTO figma_webhook_subscriptions(id,binding_id,webhook_id,event_type,passcode_hash) VALUES($1,$1,$1,'FILE_UPDATE',$2)", [id, "a".repeat(64)]); return id;
  };
  const removed = await binding(own, f.owner.user.id), preserved = await binding(foreign, f.other.user.id);
  await admin.query("REVOKE UPDATE ON connected_accounts FROM misty_hono_app_test; GRANT UPDATE(last_error_code) ON connected_accounts TO misty_hono_app_test");
  try {
    expect((await f.request(`/connections/${own}`, "DELETE")).status).toBe(500);
    expect((await f.state(own)).status).toBe("active"); expect(f.provider).not.toHaveBeenCalled();
    for (const table of ["figma_webhook_subscriptions", "provider_shared_resources", "space_integrations", "figma_space_bindings"])
      expect((await admin.query(`SELECT status FROM ${table} WHERE id=$1`, [removed])).rows[0].status).toBe("active");
  } finally { await admin.query("REVOKE UPDATE(last_error_code) ON connected_accounts FROM misty_hono_app_test; GRANT UPDATE ON connected_accounts TO misty_hono_app_test"); }
  f.provider.mockResolvedValue(new Response(null, { status: 503 }));
  expect((await f.request(`/v1/connections/${own}`, "DELETE")).status).toBe(204);
  for (const table of ["figma_webhook_subscriptions", "provider_shared_resources", "space_integrations", "figma_space_bindings"]) {
    const rows = (await admin.query(`SELECT id,status FROM ${table} WHERE id=ANY($1::text[])`, [[removed, preserved]])).rows;
    expect(rows.find((row) => row.id === removed)?.status).toBe("disabled"); expect(rows.find((row) => row.id === preserved)?.status).toBe("active");
  }
  expect(f.provider).toHaveBeenCalledTimes(1); expect(f.provider.mock.calls[0]![0]).toBe(`https://api.figma.com/v2/webhooks/${removed}`);
  expect(await f.state(own)).toMatchObject({ status: "revoked", bytes: 0 });
});
