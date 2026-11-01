import { controlPlaneRequest } from "./control-plane.js";
import { ControlPlaneError } from "./control-plane-error.js";
import { agentDeviceHook } from "./device.js";
import { classifyMCPTransportError } from "./mcp-errors.js";
import { requestMCPToolExecution } from "./mcp-runtime.js";
import { cachedRunAccess } from "./mcp-sessions.js";
import { recoverableToolError, rethrowStepError } from "./runtime-errors.js";
import { continueToolExecution, type ToolExecutionResponse } from "./tool-execution.js";
import type { MCPRunAccess, RuntimeToolContext } from "./types.js";

/** Runs one tool call in the workflow, holding device and user-action waits on
 * durable hooks. Every attempt keeps the call's effect identity. */
export async function executeTool(context: RuntimeToolContext, callId: string, name: string, input: unknown): Promise<unknown> {
  const deviceToken = (attempt: number) => `misty-device:${context.mistyRunId}:${callId}:wait:${attempt}`;
  try {
    return await continueToolExecution({
      request: (attempt) =>
        requestToolExecution(context, callId, name, input, deviceToken(attempt)),
      // Engine wake tokens are opaque. Go owns the separate user-action wait
      // and requires a trusted user decision before sending this wake signal.
      intervention: async (attempt) => (await agentDeviceHook.create({ token: deviceToken(attempt) })).available,
      device: async (attempt) => (await agentDeviceHook.create({ token: deviceToken(attempt) })).available,
    });
  } catch (error) {
    if (error instanceof ControlPlaneError) rethrowStepError(error);
    throw error;
  }
}

async function requestToolExecution(
  context: RuntimeToolContext,
  callId: string,
  name: string,
  input: unknown,
  deviceHookToken: string,
): Promise<ToolExecutionResponse> {
  "use step";
  const started = Date.now();
  try {
    // The run's bearer is reused while it has more than a minute left.
    const access = cachedRunAccess(context.mistyRunId, context.controlPlaneURL)
      ?? await controlPlaneRequest<MCPRunAccess>(context, "mcp-token", { intervention_wait_version: 1 }, `${context.mistyRunId}:mcp-token:${callId}`);
    const authorized = Date.now();
    const response = await requestMCPToolExecution(context, access, callId, name, input, deviceHookToken, await queryEmbedding(name, input));
    // One line per call for timing; never arguments or results.
    console.info(`[misty-agent-runtime] tool_timing ${JSON.stringify({ run: context.mistyRunId, call: callId, tool: name, token_ms: authorized - started, mcp_ms: Date.now() - authorized })}`);
    return response;
  } catch (error) {
    const recoverable = recoverableToolError(error);
    if (recoverable) return { tool_error: recoverable };
    if (error instanceof ControlPlaneError && !error.transient) {
      const denied = error.status === 401 || error.status === 403;
      return {
        tool_error: {
          code: denied ? "permission_denied" : "tool_unavailable",
          message: denied ? "This run is no longer authorized to use that tool." : "That tool is not available for this run.",
        },
      };
    }
    const failure = classifyMCPTransportError(error);
    if (failure.recognized && !failure.transient) return { tool_error: { code: failure.code, message: failure.message } };
    rethrowStepError(error);
  }
}

requestToolExecution.maxRetries = 2;

// Model code is loaded only inside the step above: the workflow bundle can't take it.
/** Tools that rank the person's memory by meaning; their query is embedded here. */
const meaningSearches = new Set(["memory.history.search", "memory.pref.list"]);

/**
 * On a desktop runtime the server calls no model, so a memory search's query is embedded on the
 * device's embedding model and the vector travels with the call. Without one, search finds
 * nothing, as it does on a server with no embedding model.
 */
async function queryEmbedding(name: string, input: unknown): Promise<Record<string, unknown>> {
  const query = (input as { query?: unknown } | null)?.query;
  if (process.env.MISTY_RUNTIME_DESKTOP !== "1" || !meaningSearches.has(name) || typeof query !== "string" || query.trim().length < 3) return {};
  try {
    const normalized = query.toLowerCase().split(/\s+/).filter(Boolean).join(" ");
    const { embedDeviceCall } = await import("./device-calls.js");
    const { model, embeddings } = await embedDeviceCall({ values: [normalized], dimensions: 768 });
    return { "misty/query_embedding": embeddings[0], "misty/query_embedding_model": model };
  } catch {
    return {};
  }
}

