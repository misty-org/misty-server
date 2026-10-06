import {
  Client as MCPClient,
  StreamableHTTPClientTransport,
} from "@misty/mcp-client-runtime";
import { controlPlaneRequest } from "./control-plane.js";
import { ControlPlaneError } from "./control-plane-error.js";
import { resolveMCPEndpoint } from "./mcp-endpoint.js";
import { rethrowStepError } from "./runtime-errors.js";
import type {
  MCPRunAccess,
  MCPRemoteTool,
  RuntimeToolContext,
} from "./types.js";
import { normalizeToolInputSchema } from "./tool-schema.js";
import { collectToolPages } from "./tool-discovery.js";

export async function requestMCPToolExecution(
  context: RuntimeToolContext,
  access: MCPRunAccess,
  callId: string,
  name: string,
  input: unknown,
  deviceHookToken: string,
): Promise<{
  result?: unknown;
  device_wait?: boolean;
  intervention_wait?: { id: string; action: string; reason: string };
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
  const endpoint = resolveMCPEndpoint(context.controlPlaneURL, access.mcp_path);
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
        "misty/device_hook_token": deviceHookToken,
      },
    });
    const meta = response._meta as Record<string, unknown> | undefined;
    const waitCount = Number(meta?.["misty/device_wait"] === true) + Number(Boolean(meta?.["misty/intervention_wait"]));
    if (waitCount > 1 || (waitCount > 0 && (response.isError || response.structuredContent !== undefined))) {
      return {tool_error:{code:"invalid_tool_outcome",message:"The tool returned contradictory wait or completion outcomes."}};
    }
    if (meta?.["misty/intervention_wait"]) return { intervention_wait: meta["misty/intervention_wait"] as {id:string;action:string;reason:string} };
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
    if (!text || text.type !== "text" || !text.text)
      return {
        tool_error: {
          code: "missing_tool_result",
          message: "The provider returned no execution result.",
        },
      };
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
      { intervention_wait_version: 1 },
      `${context.mistyRunId}:mcp-list`,
    );
  } catch (error) {
    if (
      error instanceof ControlPlaneError &&
      (error.status === 404 || error.status === 405 || error.status === 501)
    ) {
      return { supported: false, tools: [] };
    }
    rethrowStepError(error);
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
    const tools = await collectToolPages((cursor) =>
      client.listTools(cursor ? { cursor } : undefined),
    );
    return {
      supported: true,
      tools: tools.map((item) => ({
        name: item.name,
        description: (item.description || item.title || item.name).slice(
          0,
          2_000,
        ),
        inputSchema: normalizeToolInputSchema(item.inputSchema as Record<string, unknown>),
        readOnly: item.annotations?.readOnlyHint === true,
      })),
    };
  } finally {
    await client.close().catch(() => undefined);
  }
}

discoverRemoteMCPTools.maxRetries = 2;
