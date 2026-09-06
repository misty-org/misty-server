import type { Environment } from "../../../../../packages/runtime/src/config.js";

const clientEnvironment = {
  google: ["GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"],
  microsoft: ["MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET"],
  dropbox: ["MISTY_DROPBOX_CLIENT_ID", "MISTY_DROPBOX_CLIENT_SECRET"],
  figma: ["FIGMA_CLIENT_ID", "FIGMA_CLIENT_SECRET"],
  discord: ["DISCORD_CLIENT_ID", "DISCORD_CLIENT_SECRET"],
  instagram: ["INSTAGRAM_CLIENT_ID", "INSTAGRAM_CLIENT_SECRET"],
} as const;
export type ConnectionProvider = keyof typeof clientEnvironment;
export type ConnectionOAuthClients = Partial<Record<ConnectionProvider, { clientId: string; clientSecret: string }>>;
export function loadConnectionOAuthClients(env: Environment): ConnectionOAuthClients {
  const clients: ConnectionOAuthClients = {};
  for (const [provider, [id, secret]] of Object.entries(clientEnvironment)) {
    const clientId = env[id]?.trim(), clientSecret = env[secret]?.trim();
    if (clientId && clientSecret) clients[provider as ConnectionProvider] = { clientId, clientSecret };
  }
  return clients;
}
/** Only availability flags leave the composition root. */
export function loadConnectionProviders(env: Environment): Record<string, boolean> {
  return Object.fromEntries(Object.entries(clientEnvironment).map(([provider, keys]) => [provider, keys.every((key) => !!env[key]?.trim())]));
}
