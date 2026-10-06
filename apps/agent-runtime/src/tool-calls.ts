import { controlPlaneRequest } from "./control-plane.js";
import { ControlPlaneError } from "./control-plane-error.js";
import { agentDeviceHook } from "./device.js";
import { classifyMCPTransportError } from "./mcp-errors.js";
import { requestMCPToolExecution } from "./mcp-runtime.js";
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
        requestToolExecution(context, callId, name, input, deviceToken(attempt), `${context.mistyRunId}:tool:${callId}`),
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
  idempotencyKey: string,
): Promise<ToolExecutionResponse> {
  "use step";
  try {
    let access: MCPRunAccess;
    try {
      access = await controlPlaneRequest<MCPRunAccess>(context, "mcp-token", { intervention_wait_version: 1 }, `${context.mistyRunId}:mcp-token:${callId}`);
    } catch (error) {
      // This fallback only happens before an MCP tool call starts, so a rolling
      // deploy cannot duplicate a consequential action.
      if (error instanceof ControlPlaneError && (error.status === 404 || error.status === 405 || error.status === 501)) {
        return await controlPlaneRequest<ToolExecutionResponse>(context, "tools", {
          call_id: callId, name, arguments: input, device_hook_token: deviceHookToken,
        }, idempotencyKey);
      }
      throw error;
    }
    return await requestMCPToolExecution(context, access, callId, name, input, deviceHookToken);
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
