import {
  Client as MCPClient,
  StreamableHTTPClientTransport,
} from "@misty/mcp-client-runtime";
import { FatalError, RetryableError } from "workflow";
import { controlPlaneRequest } from "./control-plane.js";
import { ControlPlaneError } from "./control-plane-error.js";
import { classifyMCPTransportError } from "./mcp-errors.js";
import { resolveMCPEndpoint } from "./mcp-endpoint.js";
import type { MCPRunAccess, MCPRemoteTool, RuntimeToolContext } from "./types.js";

function rethrowMCPError(error: unknown): never {
  if (error instanceof ControlPlaneError) {
    if (error.transient) {
      throw new RetryableError("Misty's control plane is temporarily unavailable.", {
        retryAfter: error.status === 429 ? 5_000 : 1_000,
      });
    }
    throw new FatalError("Misty's authorization or run state changed.");
  }
  const failure = classifyMCPTransportError(error);
  if (failure.transient) {
    throw new RetryableError(failure.message, { retryAfter: failure.retryAfterMs });
  }
  if (failure.recognized) throw new FatalError(failure.message);
  throw error;
}

export async function requestMCPToolExecution(
  context: RuntimeToolContext,
  access: MCPRunAccess,
  callId: string,
  name: string,
  input: unknown,
  approvalHookToken: string,
  deviceHookToken: string,
): Promise<{
  result?: unknown;
  approval?: { id: string; state: string };
  device_wait?: boolean;
  tool_error?: { code: string; message: string };
}> {
  if (
    !access.access_token ||
    access.token_type !== "Bearer" ||
    access.protocol !== "2026-07-28" ||
    !access.mcp_path.startsWith("/")
  ) {
    throw new Error("Misty returned invalid MCP access credentials");
  }
  const endpoint = resolveMCPEndpoint(
    context.controlPlaneURL,
    access.mcp_path,
  );
  const client = new MCPClient(
    { name: "misty-vercel-agent-runtime", version: "1.0.0" },
    { versionNegotiation: { mode: "auto" } },
  );
  const transport = new StreamableHTTPClientTransport(endpoint, {
    authProvider: { token: async () => access.access_token },
  });
  try {
    await client.connect(transport);
    const response = await client.callTool({
      name,
      arguments: input as Record<string, unknown>,
      _meta: {
        "misty/call_id": callId,
        "misty/approval_hook_token": approvalHookToken,
        "misty/device_hook_token": deviceHookToken,
      },
    });
    const meta = response._meta as Record<string, unknown> | undefined;
    if (meta?.["misty/approval"]) {
      return {
        approval: meta["misty/approval"] as { id: string; state: string },
      };
    }
    if (meta?.["misty/device_wait"] === true) return { device_wait: true };
    if (response.isError) {
      const detail = response.content
        .filter(
          (item): item is Extract<typeof item, { type: "text" }> =>
            item.type === "text",
        )
        .map((item) => item.text)
        .join("; ")
        .slice(0, 500);
      return {
        tool_error: {
          code: "tool_execution_failed",
          message: detail || "Misty could not complete this tool call.",
        },
      };
    }
    if (response.structuredContent !== undefined) {
      return { result: response.structuredContent };
    }
    const text = response.content.find((item) => item.type === "text");
    if (!text || text.type !== "text" || !text.text) return { result: {} };
    try {
      return { result: JSON.parse(text.text) as unknown };
    } catch {
      return { result: text.text };
    }
  } finally {
    await client.close().catch(() => undefined);
  }
}

export async function discoverRemoteMCPTools(
  context: RuntimeToolContext,
): Promise<{ supported: boolean; tools: MCPRemoteTool[] }> {
  "use step";
  let access: MCPRunAccess;
  try {
    access = await controlPlaneRequest<MCPRunAccess>(
      context,
      "mcp-token",
      {},
      `${context.mistyRunId}:mcp-list`,
    );
  } catch (error) {
    if (
      error instanceof ControlPlaneError &&
      (error.status === 404 || error.status === 405 || error.status === 501)
    ) {
      return { supported: false, tools: [] };
    }
    rethrowMCPError(error);
  }
  if (
    !access.access_token ||
    access.token_type !== "Bearer" ||
    access.protocol !== "2026-07-28" ||
    !access.mcp_path.startsWith("/")
  ) {
    throw new Error("Misty returned invalid MCP access credentials");
  }
  const client = new MCPClient(
    { name: "misty-vercel-agent-runtime", version: "1.0.0" },
    { versionNegotiation: { mode: "auto" } },
  );
  const transport = new StreamableHTTPClientTransport(
    resolveMCPEndpoint(context.controlPlaneURL, access.mcp_path),
    { authProvider: { token: async () => access.access_token } },
  );
  try {
    await client.connect(transport);
    const result = await client.listTools();
    return {
      supported: true,
      tools: result.tools.slice(0, 100).map((item) => ({
        name: item.name,
        description: (item.description || item.title || item.name).slice(
          0,
          2_000,
        ),
        inputSchema: item.inputSchema as Record<string, unknown>,
      })),
    };
  } finally {
    await client.close().catch(() => undefined);
  }
}

discoverRemoteMCPTools.maxRetries = 2;
