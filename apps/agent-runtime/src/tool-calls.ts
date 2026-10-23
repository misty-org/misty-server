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
    const response = await requestMCPToolExecution(context, access, callId, name, input, deviceHookToken);
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
