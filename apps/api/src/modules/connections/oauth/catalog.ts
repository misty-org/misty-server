import { createHash } from "node:crypto";
import type { ConnectionProvider } from "../config.js";

type Definition = { name: string; authorize: string; identity: string; base: string[]; defaults: string[];
  capabilities: Record<string, string[]>; params?: Record<string, string>; pkce?: false; identityPost?: true; identityQueryToken?: true };
export const connectionOAuthCatalog: Record<ConnectionProvider, Definition> = {
  google: { name: "Google", authorize: "https://accounts.google.com/o/oauth2/v2/auth", identity: "https://openidconnect.googleapis.com/v1/userinfo",
    base: ["openid", "email", "profile"], defaults: ["mail"], params: { access_type: "offline", include_granted_scopes: "true", prompt: "consent" },
    capabilities: { mail: ["https://www.googleapis.com/auth/gmail.modify", "https://www.googleapis.com/auth/gmail.send"],
      calendar_read: ["https://www.googleapis.com/auth/calendar.readonly"], calendar_write: ["https://www.googleapis.com/auth/calendar.readonly", "https://www.googleapis.com/auth/calendar.events"], files: ["https://www.googleapis.com/auth/drive"] } },
  microsoft: { name: "Microsoft", authorize: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", identity: "https://graph.microsoft.com/v1.0/me?$select=id,displayName,userPrincipalName,mail",
    base: ["openid", "profile", "email", "offline_access", "User.Read"], defaults: ["mail"], params: { prompt: "select_account", response_mode: "query" },
    capabilities: { mail: ["Mail.ReadWrite", "Mail.Send"], files: ["Files.ReadWrite.All"] } },
  dropbox: { name: "Dropbox", authorize: "https://www.dropbox.com/oauth2/authorize", identity: "https://api.dropboxapi.com/2/users/get_current_account", identityPost: true,
    base: [], defaults: ["files"], params: { token_access_type: "offline", include_granted_scopes: "user" },
    capabilities: { files: ["account_info.read", "files.metadata.read", "files.content.read", "files.content.write"] } },
  figma: { name: "Figma", authorize: "https://www.figma.com/oauth", identity: "https://api.figma.com/v1/me", base: ["current_user:read"], defaults: ["drawings_read"],
    capabilities: { drawings_read: ["file_metadata:read", "file_content:read", "file_versions:read", "file_comments:read"], drawings_comments: ["file_comments:write"], drawings_projects: ["folders:read"], drawings_webhooks: ["webhooks:write"] } },
  discord: { name: "Discord", authorize: "https://discord.com/oauth2/authorize", identity: "https://discord.com/api/v10/users/@me", base: ["identify", "guilds", "bot"],
    defaults: ["social_read", "social_send"], params: { permissions: "68608" },
    capabilities: { social_read: ["identify", "guilds"], social_send: ["identify", "guilds"], social_automation: ["identify", "guilds"] } },
  instagram: { name: "Instagram", authorize: "https://www.instagram.com/oauth/authorize", identity: "https://graph.instagram.com/me?fields=id,username", pkce: false, identityQueryToken: true,
    base: ["instagram_business_basic", "instagram_business_manage_messages"], defaults: ["social_read", "social_send"],
    capabilities: { social_read: ["instagram_business_basic", "instagram_business_manage_messages"], social_send: ["instagram_business_basic", "instagram_business_manage_messages"], social_automation: ["instagram_business_basic", "instagram_business_manage_messages"] } },
};
export class ConnectionAuthorizationError extends Error {
  constructor(readonly code: "invalid_request" | "provider_not_configured" | "authorization_expired" | "authorization_changed" | "authorization_denied" | "authorization_unavailable" | "authorization_limit" | "provider_permissions_missing") { super(code); this.name = "ConnectionAuthorizationError"; }
}
export function connectionProvider(value: string): ConnectionProvider {
  if (!Object.hasOwn(connectionOAuthCatalog, value)) throw new ConnectionAuthorizationError("invalid_request");
  return value as ConnectionProvider;
}
export const sorted = (values: string[]) => [...new Set(values)].sort();
export function requestedAccess(provider: ConnectionProvider, input: string[]) {
  const definition = connectionOAuthCatalog[provider], capabilities = sorted((input.length ? input : definition.defaults).map((value) => value.trim().toLowerCase()));
  if (capabilities.some((value) => !Object.hasOwn(definition.capabilities, value))) throw new ConnectionAuthorizationError("invalid_request");
  return { capabilities, scopes: sorted([...definition.base, ...capabilities.flatMap((value) => definition.capabilities[value]!)]) };
}
/** A new token replaces its predecessor's grants; stored history is not evidence of current consent. */
export function grantedAccess(provider: ConnectionProvider, requested: string[], candidates: string[], scope?: string) {
  const scopes = sorted(scope === undefined ? requested : scope.split(/[\s,]+/).filter(Boolean));
  const canonical = (value: string) => provider === "microsoft" ? value.replace(/^https:\/\/graph.microsoft.com\//i, "").toLowerCase() : value;
  const granted = new Set(scopes.map(canonical));
  const capabilities = Object.entries(connectionOAuthCatalog[provider].capabilities).filter(([name, required]) => candidates.includes(name) && required.every((value) => granted.has(canonical(value)))).map(([name]) => name).sort();
  if (!capabilities.length) throw new ConnectionAuthorizationError("provider_permissions_missing");
  return { capabilities, scopes };
}
export function authorizationUrl(provider: ConnectionProvider, clientId: string, redirect: string, state: string, verifier: string, scopes: string[]) {
  const definition = connectionOAuthCatalog[provider], url = new URL(definition.authorize);
  const params = { client_id: clientId, redirect_uri: redirect, response_type: "code", state, scope: scopes.join(" "), ...definition.params };
  for (const [key, value] of Object.entries(params)) url.searchParams.set(key, value);
  if (definition.pkce !== false) { url.searchParams.set("code_challenge", createHash("sha256").update(verifier).digest("base64url")); url.searchParams.set("code_challenge_method", "S256"); }
  return url.href;
}
