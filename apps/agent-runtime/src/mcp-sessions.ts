import { Client as MCPClient, StreamableHTTPClientTransport } from "@misty/mcp-client-runtime";
import { resolveMCPEndpoint } from "./mcp-endpoint.js";
import type { MCPRunAccess } from "./types.js";

/**
 * One MCP session per run instead of one per tool call (docs/agents/harness.md,
 * Device jobs). A run keeps its five-minute bearer until a minute before it
 * expires and one connected client, so most calls skip the token exchange and
 * the MCP handshake. Go still authorizes every request against the run's
 * binding; the bearer only ever names this run. A session missing here (a
 * different process, or after an idle close) is simply rebuilt.
 */
interface Session {
  access: MCPRunAccess;
  expiresAt: number;
  endpoint: string;
  client?: Promise<MCPClient>;
  idle?: ReturnType<typeof setTimeout>;
}

const sessions = new Map<string, Session>();
const renewBeforeMs = 60_000;
const idleCloseMs = 120_000;
const maxSessions = 256;

function sessionKey(runId: string, controlPlaneURL: string) {
  return `${runId}\n${controlPlaneURL}`;
}

/** A cached bearer for the run, when it still has more than a minute left. */
export function cachedRunAccess(runId: string, controlPlaneURL: string, now = Date.now()): MCPRunAccess | undefined {
  const session = sessions.get(sessionKey(runId, controlPlaneURL));
  return session && session.expiresAt - renewBeforeMs > now ? session.access : undefined;
}

/** Stores a freshly issued bearer; the run's client picks it up on its next request. */
export function rememberRunAccess(runId: string, controlPlaneURL: string, access: MCPRunAccess, now = Date.now()) {
  const key = sessionKey(runId, controlPlaneURL);
  const endpoint = resolveMCPEndpoint(controlPlaneURL, access.mcp_path).toString();
  const expiresAt = now + Math.max(0, access.expires_in) * 1000;
  const existing = sessions.get(key);
  if (existing && existing.endpoint === endpoint) {
    existing.access = access;
    existing.expiresAt = expiresAt;
    return;
  }
  if (existing) void closeRunSession(runId, controlPlaneURL);
  if (sessions.size >= maxSessions) {
    const oldest = sessions.keys().next().value;
    if (oldest) closeKey(oldest);
  }
  sessions.set(key, { access, expiresAt, endpoint });
}

function closeKey(key: string) {
  const session = sessions.get(key);
  sessions.delete(key);
  if (!session) return;
  clearTimeout(session.idle);
  void session.client?.then((client) => client.close()).catch(() => undefined);
}

export function closeRunSession(runId: string, controlPlaneURL: string) {
  closeKey(sessionKey(runId, controlPlaneURL));
}

/** The run's connected client, connecting once. The bearer is read per request. */
export async function runClient(runId: string, controlPlaneURL: string): Promise<MCPClient> {
  const key = sessionKey(runId, controlPlaneURL);
  const session = sessions.get(key);
  if (!session) throw new Error("mcp_session_missing");
  clearTimeout(session.idle);
  session.idle = setTimeout(() => closeKey(key), idleCloseMs);
  session.idle.unref?.();
  session.client ??= (async () => {
    const client = new MCPClient({ name: "misty-vercel-agent-runtime", version: "1.0.0" }, { versionNegotiation: { mode: "auto" } });
    const transport = new StreamableHTTPClientTransport(new URL(session.endpoint), {
      authProvider: { token: async () => sessions.get(key)?.access.access_token ?? session.access.access_token },
    });
    await client.connect(transport);
    return client;
  })();
  try {
    return await session.client;
  } catch (error) {
    // A failed connection is never reused; the next call connects again.
    if (sessions.get(key) === session) session.client = undefined;
    throw error;
  }
}

export function sessionCountForTests() {
  return sessions.size;
}
