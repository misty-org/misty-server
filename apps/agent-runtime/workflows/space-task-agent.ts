import { routineWaitAdapter } from "../src/routine-wait-adapter.js";
import { routineAgentAdapter, type RoutineAgentControl } from "../src/routine-agent-adapter.js";
import type { RoutineAgentResult } from "../src/routine-agent.js";
import type { LanguageModelUsage } from "ai";
import { executeRoutine } from "../src/routine-coordinator.js";
import { MISTY_HARNESS_VERSION, type HarnessCheckpoint, type HarnessCompletion, type HarnessExecution } from "../src/harness.js";
import { executePinnedCapability } from "../src/pinned-capability.js";
import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, jsonSchema, tool, type StopCondition, type ModelMessage } from "ai";
import { FatalError, getWorkflowMetadata, RetryableError, sleep } from "workflow";
import { z } from "zod";
import {
  controlPlaneRequest,
  type RuntimeIdentity,
} from "../src/control-plane.js";
import { ControlPlaneError } from "../src/control-plane-error.js";
import { classifyMCPTransportError } from "../src/mcp-errors.js";
export { classifyMCPTransportError } from "../src/mcp-errors.js";
import {
  discoverRemoteMCPTools,
  requestMCPToolExecution,
} from "../src/mcp-runtime.js";
import { agentToolApprovalHook } from "../src/approval.js";
import { agentDeviceHook } from "../src/device.js";
import {
  capabilityPage,
  capabilityToolKey,
} from "../src/capability-selection.js";
import { continueToolExecution } from "../src/tool-execution.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { accumulateModelUsage, modelTimeout, type ExecutionBudget } from "../src/model-budget.js";
import type {
  MCPRunAccess,
  RuntimeToolContext,
  SpaceTaskContext,
} from "../src/types.js";

export interface SpaceTaskWorkflowInput {
  adapterVersion?: typeof MISTY_HARNESS_VERSION;
  mistyRunId: string;
  controlPlaneURL: string;
}

function rethrowStepError(error: unknown): never {
  if (error instanceof ControlPlaneError) {
    if (error.code === "agent_model_turn_limit" || error.code === "agent_execution_time_limit") throw new FatalError(error.code);
    if (error.transient) {
      throw new RetryableError(
        "Misty's control plane is temporarily unavailable.",
        {
          retryAfter: error.status === 429 ? 5_000 : 1_000,
        },
      );
    }
    throw new FatalError("Misty's authorization or run state changed.");
  }
  const failure = classifyMCPTransportError(error);
  if (failure.transient) {
    throw new RetryableError(failure.message, {
      retryAfter: failure.retryAfterMs,
    });
  }
  if (failure.recognized) throw new FatalError(failure.message);
  throw error;
}

export function recoverableToolError(error: unknown): {
  code: string;
  message: string;
} | null {
  if (!(error instanceof ControlPlaneError)) return null;
  if (error.code === "agent_model_turn_limit" || error.code === "agent_execution_time_limit") return null;
  if (![400, 409, 422].includes(error.status)) return null;
  return {
    code: error.code || "invalid_tool_input",
    message: error.message
      .replace(/^[a-z0-9_]+:\s*/i, "")
      .trim()
      .slice(0, 500),
  };
}

export function classifyRuntimeError(error: unknown): {
  code: string;
  message: string;
} {
  const message =
    error instanceof Error ? error.message : "Agent workflow failed";
  const normalized =
    `${error instanceof Error ? error.name : ""} ${message}`.toLowerCase();
  if (normalized.includes("agent_model_turn_limit")) {
    return { code: "agent_model_turn_limit", message: "This run reached its model-turn limit. Review its completed work before starting another request." };
  }
  if (normalized.includes("agent_execution_time_limit")) {
    return { code: "agent_execution_time_limit", message: "This run used its execution-time allowance. Review its completed work before starting another request." };
  }
  if (
    normalized.includes("timeout") ||
    normalized.includes("timed out") ||
    normalized.includes("aborted")
  ) {
    return {
      code: "agent_runtime_timeout",
      message: "Misty timed out before completing this request.",
    };
  }
  if (normalized.includes("hosted_ai_limit_reached")) {
    return {
      code: "hosted_ai_limit_reached",
      message:
        "Your weekly AI agent allowance is fully used. Try again after it resets.",
    };
  }
  if (error instanceof ControlPlaneError && !error.transient) {
    return {
      code: "authorization_or_state_changed",
      message: "Misty's authorization or run state changed.",
    };
  }
  if (
    normalized.includes("gatewayinternalservererror") ||
    normalized.includes("service temporarily unavailable")
  ) {
    return {
      code: "model_gateway_unavailable",
      message:
        "Misty's model providers are temporarily unavailable. Please try again shortly.",
    };
  }
  return {
    code: "agent_runtime_failed",
    message: "Misty could not complete this request.",
  };
}

function fallbackModels(primaryModel: string): string[] {
  return ["poolside/laguna-s-2.1", "google/gemini-3-flash"].filter(
    (model) => model !== primaryModel,
  );
}

export function stoppedAtModelTurnLimit(stepCount: number, finishReason: string): boolean {
  return stepCount >= 20 && finishReason !== "stop";
}

export function unfinishedModelResult(
  aborted: boolean,
  stepCount: number,
  finishReason: string,
): { code: string; message: string } | null {
  // WorkflowAgent can resolve an aborted stream with the last completed step's
  // finish reason. A normal return (or earlier text) is not completion evidence.
  if (aborted) {
    return { code: "agent_runtime_interrupted", message: "This run stopped before finishing. Review its completed work before starting another request." };
  }
  if (stoppedAtModelTurnLimit(stepCount, finishReason)) {
    return { code: "agent_model_turn_limit", message: "This run reached its model-turn limit before finishing. Review its completed work before starting another request." };
  }
  if (finishReason === "stop" && stepCount > 0) return null;
  if (finishReason === "length") {
    return { code: "agent_response_incomplete", message: "The model reached its response limit before finishing. Review the partial result before continuing." };
  }
  return { code: "agent_response_incomplete", message: "The model did not finish this request. Review its completed work before starting another request." };
}

export function classifyToolCompletion(toolNames: string[]): {
  status: "success" | "incomplete";
  error_code?: string;
  error_message?: string;
} {
  const failed = [
    ...new Set(toolNames.map((name) => name.trim()).filter(Boolean)),
  ];
  if (failed.length === 0) return { status: "success" };
  return {
    status: "incomplete",
    error_code: "tool_execution_failed",
    error_message: `Could not complete ${failed.join(", ")}.`,
  };
}

export function incompleteToolResultText(
  failures: Array<{ toolName: string; error: string }>,
): string {
  const details = failures
    .slice(0, 3)
    .map(({ toolName, error }) => {
      const label = toolName.replace(/[._]/g, " ");
      const reason =
        error.trim().slice(0, 300) || "The action could not be completed.";
      return `${label}: ${reason}`;
    })
    .join("; ");
  return `I couldn't fully complete that request. ${details || "A required action failed."}`;
}

export const proactiveExecutionInstructions = `Execution contract:
- Treat a concrete imperative request as work to perform now, not as a request for instructions or a proposal.
- Privately break multi-part requests into the necessary steps and continue until every requested action and deliverable is complete or a real blocker prevents progress.
- When creating or updating an artifact with generated content, compose the complete final content before the write. Check every explicit constraint—including title, format, count, length, and requested sections—and send the full result in the tool call. Never create a placeholder, outline, partial draft, or empty shell when the user requested finished content.
- Every tool named as required by the control plane represents an explicitly requested effect. Call it, inspect its confirming result, and do not claim success without that evidence.
- Do not ask the user to restate, plan, create subtasks, or say “finish” when the request already contains enough detail. Ask one focused clarification only when a missing choice would materially change the result.
- If any requested part cannot be completed, say exactly what remains incomplete and why.`;

export function missingRequiredToolCalls(
  requiredToolNames: Iterable<string>,
  successfulToolNames: Iterable<string>,
): string[] {
  const successful = new Set(successfulToolNames);
  return [...new Set(requiredToolNames)]
    .map((name) => name.trim())
    .filter((name) => name !== "" && !successful.has(name));
}

export function incompleteRequiredToolText(toolNames: string[]): string {
  const labels = toolNames.slice(0, 4).map(humanToolActionLabel).join(", ");
  return `I couldn't fully complete that request. The required ${labels || "action"} action did not finish.`;
}

export function confirmedActionFallbackText(toolNames: string[]): string {
  const labels = toolNames.slice(0, 4).map(humanToolActionLabel).join(", ");
  return labels
    ? `Done — I completed the requested ${labels} action${toolNames.length === 1 ? "" : "s"}.`
    : "Done — I completed the requested action.";
}

function humanToolActionLabel(name: string): string {
  return (
    {
      "notes.create": "note creation",
      "notes.update": "note update",
      "tasks.create": "task creation",
      "tasks.update": "task update",
      "calendar.create": "event creation",
      "calendar.update": "event update",
      "messages.send": "message sending",
      "drawings.create": "drawing creation",
      "drawings.apply": "drawing update",
      "roadmaps.create": "roadmap creation",
      "roadmaps.update": "roadmap update",
      "library.update": "Library update",
      "library.promote_attachment": "attachment save",
      "agents.delegate": "delegation",
      "memory.remember": "memory save",
      "memory.forget": "memory removal",
    }[name] ?? name.replace(/[._]/g, " ")
  );
}

export function unconfirmedToolResultReason(output: unknown): string {
  if (output === undefined)
    return "The requested action returned no confirmed result.";
  if (!output || typeof output !== "object") return "";
  const result = output as Record<string, unknown>;
  if (result.status === "uncertain")
    return "The action may have happened, but its outcome could not be verified.";
  if (
    [
      "failure",
      "approval_required",
      "device_required",
      "user_intervention_required",
    ].includes(String(result.status))
  )
    return "The requested action has not completed.";
  if (result.denied === true) {
    return "The requested action was not approved.";
  }
  if (result.unavailable === true) {
    return "The device required for this action was unavailable.";
  }
  return "";
}

function stableToolValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableToolValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, item]) => [key, stableToolValue(item)]),
    );
  }
  return value;
}

export function toolFailureSignature(toolName: string, input: unknown): string {
  let encoded = "";
  try {
    encoded = JSON.stringify(stableToolValue(input));
  } catch {
    encoded = "[unserializable]";
  }
  return `${toolName}:${encoded}`;
}

function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  try {
    return JSON.stringify(error);
  } catch {
    return "";
  }
}

export const stopOnRepeatedOrTerminalToolFailure: StopCondition<any> = ({
  steps,
}) => {
  const counts = new Map<string, number>();
  for (const step of steps) {
    for (const part of step.content) {
      if (
        part.type === "tool-result" &&
        part.output &&
        typeof part.output === "object" &&
        ["uncertain", "user_intervention_required"].includes(String((part.output as Record<string, unknown>).status))
      )
        return true;
      if (part.type !== "tool-error") continue;
      const signature = toolFailureSignature(part.toolName, part.input);
      const count = (counts.get(signature) ?? 0) + 1;
      counts.set(signature, count);
      const message = errorText(part.error).toLowerCase();
      if (
        message.includes("tool_unavailable") ||
        message.includes("permission_denied") ||
        message.includes("authorization_or_state_changed") ||
        message.includes("agent_execution_time_limit") ||
        count >= 2
      ) {
        return true;
      }
    }
  }
  return false;
};

async function activateRuntime(identity: RuntimeIdentity): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(
      identity,
      "activate",
      { runtime_kind: "vercel-workflow" },
      `${identity.mistyRunId}:activate`,
    );
  } catch (error) {
    rethrowStepError(error);
  }
}

async function fetchContext(
  identity: RuntimeIdentity,
): Promise<SpaceTaskContext> {
  "use step";
  try {
    return await controlPlaneRequest<SpaceTaskContext>(
      identity,
      "context",
      { routine_protocol: 1, routine_agent_protocol: 1, routine_wait_protocol: 1 },
      `${identity.mistyRunId}:context`,
    );
  } catch (error) {
    rethrowStepError(error);
  }
}

async function openRoutineWait(identity: RuntimeIdentity, stepId: string, until: string): Promise<unknown> {
  "use step";
  try {
    return await controlPlaneRequest(identity, "routine-wait", {phase: "open", step_id: stepId, until}, `${identity.mistyRunId}:routine-wait:${stepId}:open`);
  } catch (error) { rethrowStepError(error); }
}
async function resumeRoutineWait(identity: RuntimeIdentity, stepId: string, waitId: string): Promise<unknown> {
  "use step";
  try {
    return await controlPlaneRequest(identity, "routine-wait", {phase: "resume", step_id: stepId, wait_id: waitId}, `${identity.mistyRunId}:routine-wait:${waitId}:resume`);
  } catch (error) { rethrowStepError(error); }
}

async function openRoutineAgent(identity: RuntimeIdentity, stepId: string): Promise<Awaited<ReturnType<RoutineAgentControl["open"]>>> {
  "use step";
  try {
    return await controlPlaneRequest(identity, "routine-agent", {phase: "open", step_id: stepId}, `${identity.mistyRunId}:routine-agent:${stepId}:open`);
  } catch (error) { rethrowStepError(error); }
}
async function finishRoutineAgent(identity: RuntimeIdentity, stepId: string, result: RoutineAgentResult): Promise<unknown> {
  "use step";
  try {
    return await controlPlaneRequest(identity, "routine-agent", {phase: "finish", step_id: stepId, result}, `${identity.mistyRunId}:routine-agent:${stepId}:finish`);
  } catch (error) { rethrowStepError(error); }
}
async function beginRoutineModel(identity: RuntimeIdentity, nodeId: string): Promise<ExecutionBudget> {
  "use step";
  try {
    return await controlPlaneRequest(identity, "routine-agent", {phase: "model_start", node_id: nodeId}, `${identity.mistyRunId}:${nodeId}:start`);
  } catch (error) { rethrowStepError(error); }
}
async function finishRoutineModel(identity: RuntimeIdentity, nodeId: string, usage: LanguageModelUsage): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(identity, "routine-agent", {phase: "model_finish", node_id: nodeId, usage}, `${identity.mistyRunId}:${nodeId}:finish`);
  } catch (error) { rethrowStepError(error); }
}

async function fetchExecutionBudget(identity: RuntimeIdentity, turn: number): Promise<ExecutionBudget> {
  "use step";
  try {
    return await controlPlaneRequest<ExecutionBudget>(identity, "budget", { begin: true }, `${identity.mistyRunId}:budget:${turn}`);
  } catch (error) {
    rethrowStepError(error);
  }
}

async function executeTool(
  context: RuntimeToolContext,
  callId: string,
  name: string,
  input: unknown,
): Promise<unknown> {
  try {
    const approvalToken = (attempt: number) =>
      `misty:${context.mistyRunId}:${callId}:wait:${attempt}`;
    const deviceToken = (attempt: number) =>
      `misty-device:${context.mistyRunId}:${callId}:wait:${attempt}`;
    return await continueToolExecution({
      request: (attempt) =>
        requestToolExecution(
          context,
          callId,
          name,
          input,
          approvalToken(attempt),
          deviceToken(attempt),
          `${context.mistyRunId}:tool:${callId}`,
        ),
      approval: async (_approval, attempt) => {
        const decision = await agentToolApprovalHook.create({
          token: approvalToken(attempt),
        });
        return decision.approved;
      },
      intervention: async (attempt) => {
        // Engine wake tokens are opaque. Go owns the separate user-action wait
        // and requires a trusted user decision before sending this wake signal.
        const decision = await agentDeviceHook.create({ token: deviceToken(attempt) });
        return decision.available;
      },
      device: async (attempt) => {
        const device = await agentDeviceHook.create({
          token: deviceToken(attempt),
        });
        return device.available;
      },
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
  approvalHookToken: string,
  deviceHookToken: string,
  idempotencyKey: string,
): Promise<{
  result?: unknown;
  approval?: { id: string; state: string };
  device_wait?: boolean;
  intervention_wait?: { id: string; action: string; reason: string };
  tool_error?: { code: string; message: string };
}> {
  "use step";
  try {
    let access: MCPRunAccess;
    try {
      access = await controlPlaneRequest<MCPRunAccess>(
        context,
        "mcp-token",
        { intervention_wait_version: 1 },
        `${context.mistyRunId}:mcp-token:${callId}`,
      );
    } catch (error) {
      // This fallback only happens before an MCP tool call starts, so a rolling
      // deploy cannot duplicate a consequential action.
      if (
        error instanceof ControlPlaneError &&
        (error.status === 404 || error.status === 405 || error.status === 501)
      ) {
        return await requestLegacyToolExecution(
          context,
          callId,
          name,
          input,
          approvalHookToken,
          deviceHookToken,
          idempotencyKey,
        );
      }
      throw error;
    }
    return await requestMCPToolExecution(
      context,
      access,
      callId,
      name,
      input,
      approvalHookToken,
      deviceHookToken,
    );
  } catch (error) {
    const recoverable = recoverableToolError(error);
    if (recoverable) return { tool_error: recoverable };
    if (error instanceof ControlPlaneError && !error.transient) {
      return {
        tool_error: {
          code:
            error.status === 401 || error.status === 403
              ? "permission_denied"
              : "tool_unavailable",
          message:
            error.status === 401 || error.status === 403
              ? "This run is no longer authorized to use that tool."
              : "That tool is not available for this run.",
        },
      };
    }
    const failure = classifyMCPTransportError(error);
    if (failure.recognized && !failure.transient) {
      return {
        tool_error: { code: failure.code, message: failure.message },
      };
    }
    rethrowStepError(error);
  }
}

requestToolExecution.maxRetries = 2;

async function requestLegacyToolExecution(
  context: RuntimeToolContext,
  callId: string,
  name: string,
  input: unknown,
  approvalHookToken: string,
  deviceHookToken: string,
  idempotencyKey: string,
): Promise<{
  result?: unknown;
  approval?: { id: string; state: string };
  device_wait?: boolean;
  intervention_wait?: { id: string; action: string; reason: string };
  tool_error?: { code: string; message: string };
}> {
  return await controlPlaneRequest(
    context,
    "tools",
    {
      call_id: callId,
      name,
      arguments: input,
      approval_hook_token: approvalHookToken,
      device_hook_token: deviceHookToken,
    },
    idempotencyKey,
  );
}

async function executeNamedTool(
  name: string,
  input: Record<string, unknown>,
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  return executeTool(options.context, options.toolCallId, name, input);
}

async function checkpoint(
  identity: RuntimeIdentity,
  event: HarnessCheckpoint,
): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(
      identity,
      "events",
      { attempt: 1, output: {}, ...event },
      `${identity.mistyRunId}:event:${event.node_id}:${event.state}`,
    );
  } catch (error) {
    rethrowStepError(error);
  }
}

async function complete(
  identity: RuntimeIdentity,
  result: HarnessCompletion,
): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(
      identity,
      "complete",
      { ...result },
      `${identity.mistyRunId}:complete`,
    );
  } catch (error) {
    rethrowStepError(error);
  }
}

function finalText(steps: Array<{ text?: string }>): string {
  for (let index = steps.length - 1; index >= 0; index -= 1) {
    const text = steps[index]?.text?.trim();
    if (text) return text;
  }
  return "";
}

function visibleErrorMessage(error: unknown): string {
  const message = errorText(error).toLowerCase();
  if (message.includes("target_not_grounded")) {
    return "Misty couldn't confirm which item to change. Open, search for, or name the exact item and try again.";
  }
  if (message.includes("invalid_tool_input")) {
    return "The tool arguments were invalid.";
  }
  if (
    message.includes("permission_denied") ||
    message.includes("authorization_or_state_changed")
  ) {
    return "This run is no longer authorized to use that tool.";
  }
  if (
    message.includes("tool_unavailable") ||
    message.includes("unknown tool")
  ) {
    return "That tool is not available for this run.";
  }
  if (message.includes("conflict")) {
    return "The item changed while Misty was working. Please retry.";
  }
  if (
    message.includes("rate_limited") ||
    message.includes("too many requests")
  ) {
    return "Misty's tool service is busy. Please try again shortly.";
  }
  if (
    message.includes("timeout") ||
    message.includes("temporarily unavailable")
  ) {
    return "Misty's tool service is temporarily unavailable.";
  }
  return "The tool could not complete this action.";
}

export async function runSpaceTaskAgent(input: SpaceTaskWorkflowInput) {
  "use workflow";
  if (input.adapterVersion && input.adapterVersion !== MISTY_HARNESS_VERSION) throw new FatalError("harness_version_unavailable");
  const runtimeRunId = getWorkflowMetadata().workflowRunId;
  const identity: RuntimeIdentity = {
    mistyRunId: input.mistyRunId,
    runtimeRunId,
    controlPlaneURL: input.controlPlaneURL,
  };
  const execution: HarnessExecution = {
    executeCapability: (callId, name, input) => executeTool(identity, callId, name, input),
    checkpoint: (event) => checkpoint(identity, event),
    complete: (result) => complete(identity, result),
  };
  await activateRuntime(identity);
  let context: SpaceTaskContext;
  try {
    context = await fetchContext(identity);
  } catch (error) {
    const failure = classifyRuntimeError(error);
    await execution.complete({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
    throw error;
  }
  const mcpCatalog = await discoverRemoteMCPTools(identity);
  const advertisedMCPTools = new Set(
    mcpCatalog.tools.map((descriptor) => descriptor.name),
  );
  if (mcpCatalog.supported) {
    const missingAllowedTools = context.allowed_tools.filter(
      (name) => !advertisedMCPTools.has(name),
    );
    await execution.checkpoint({
      node_id: "mcp:catalog",
      state: "completed",
      phase: "tools_ready",
      progress: 8,
      output: {
        advertised_tool_count: advertisedMCPTools.size,
        missing_allowed_tools: missingAllowedTools,
      },
    });
  }
  if (!mcpCatalog.supported) {
    await execution.complete({
      status: "failed",
      text: "",
      error_code: "capability_registry_unavailable",
      error_message:
        "This runtime requires the shared capability registry. Keep the pinned previous runtime available while upgrading.",
    });
    throw new FatalError("Misty's capability registry is unavailable.");
  }
  if (context.routine_execution !== undefined) {
    try {
      if (context.sdk_execution) throw new Error("conflicting_pinned_workloads");
      const report = await executeRoutine(context.routine_execution, input.mistyRunId, advertisedMCPTools, {
        wait: routineWaitAdapter({
          open: (stepId, until) => openRoutineWait(identity, stepId, until),
          resume: (stepId, waitId) => resumeRoutineWait(identity, stepId, waitId),
          sleep,
        }),
        agent: routineAgentAdapter(mcpCatalog.tools, {
          open: stepId => openRoutineAgent(identity, stepId),
          finish: (stepId, result) => finishRoutineAgent(identity, stepId, result),
          beginModel: nodeId => beginRoutineModel(identity, nodeId),
          finishModel: (nodeId, usage) => finishRoutineModel(identity, nodeId, usage),
          executeCapability: execution.executeCapability,
        }),
        executeCapability: execution.executeCapability,
        beforeStep: async (id, index, activeSeconds) => {
          const budget = await fetchExecutionBudget(identity, 100 + index);
          if (budget.version !== 1 || !budget.active || !Number.isFinite(budget.remaining_ms) || budget.remaining_ms <= 0 || budget.remaining_ms > activeSeconds * 1000) throw new FatalError("routine_execution_budget_invalid");
          await execution.checkpoint({ node_id: `routine:${id}`, state: "running", phase: "routine_step", progress: Math.min(90, 10 + index) });
        },
        checkpoint: (id, state, callId) => execution.checkpoint({
          node_id: `routine:${id}`, state: state === "failed" || state === "uncertain" ? "failed" : "completed",
          phase: `routine_${state}`, progress: 90, output: { step_id: id, state, ...(callId ? { call_id: callId } : {}) },
        }),
      });
      await execution.complete({ status: report.state === "completed" ? "success" : report.state === "failed" ? "failed" : "incomplete",
        text: report.state === "completed" ? "The routine finished its requested steps." : "The routine stopped with unfinished or partial work. Review its step history.",
        ...(report.code ? { error_code: report.code, error_message: "Review the routine's completed steps before recovery." } : {}),
      });
      return report;
    } catch (error) {
      const failure = classifyRuntimeError(error);
      await execution.complete({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
      throw error;
    }
  }
  if (context.sdk_execution) {
    try {
      await executePinnedCapability(
        context.sdk_execution,
        advertisedMCPTools,
        execution.executeCapability,
      );
      // Go derives completion from its protected effect journal, including
      // denial and uncertainty. A successful transport is not proof of an effect.
      await execution.complete({ status: "success", text: "" });
    } catch (error) {
      const failure = classifyRuntimeError(error);
      await execution.complete({
        status: "failed",
        text: "",
        error_code: failure.code,
        error_message: failure.message,
      });
    }
    return;
  }
  const descriptors = mcpCatalog.tools;
  const keyForName = new Map(
    descriptors.map((descriptor, index) => [
      descriptor.name,
      capabilityToolKey(index),
    ]),
  );
  let selectedNames = new Set(
    capabilityPage(descriptors, context.prompt).items.map((item) => item.name),
  );
  // Required actions take priority in the initial working set. Discovery remains
  // available on every turn and can expose any other admitted capability.
  selectedNames = new Set(
    [
      ...(context.required_tools ?? []).filter((name) => keyForName.has(name)),
      ...selectedNames,
    ].slice(0, 31),
  );
  const capabilityTools = Object.fromEntries(
    descriptors.map((descriptor, index) => [
      capabilityToolKey(index),
      tool({
        description: descriptor.description,
        inputSchema: jsonSchema(
          descriptor.inputSchema as Parameters<typeof jsonSchema>[0],
        ),
        execute: (value, options) =>
          execution.executeCapability(options.toolCallId, descriptor.name, value),
      }),
    ]),
  );
  const tools = {
    ...capabilityTools,
    misty_discover_capabilities: tool({
      description:
        "Search or page through every authorized capability. This activates the returned capabilities for your next turn. Use nextCursor to see further results; the initial working set is not the full catalog.",
      inputSchema: z.object({
        query: z.string().max(1000),
        cursor: z.number().int().min(0).optional(),
      }),
      execute: async ({ query, cursor }) => {
        const page = capabilityPage(descriptors, query, cursor);
        selectedNames = new Set(page.items.map((item) => item.name));
        return {
          capabilities: page.items.map((item) => ({
            name: item.name,
            description: item.description,
          })),
          total: page.total,
          nextCursor: page.nextCursor,
        };
      },
    }),
  };
  const controlPlaneNames: Record<string, string> = Object.fromEntries(
    descriptors.map((descriptor, index) => [
      capabilityToolKey(index),
      descriptor.name,
    ]),
  );
  const activeToolKeys = () =>
    [
      "misty_discover_capabilities",
      ...[...selectedNames]
        .map((name) => keyForName.get(name)!)
        .filter(Boolean),
    ] as Array<keyof typeof tools>;
  const failedToolCalls = new Map<
    string,
    {
      callId: string;
      toolName: string;
      error: string;
    }
  >();
  const toolCallSignatures = new Map<
    string,
    {
      signature: string;
      canonicalName: string;
    }
  >();
  const successfulToolCalls = new Set<string>();
  const toolExecutionOrder = serialToolLifecycle();
  let modelTurn = 0;
  const agent = new WorkflowAgent({
    id: "misty-space-task-agent",
    model: context.model_id,
    instructions: `${context.system}\n\n${proactiveExecutionInstructions}\nUse misty_discover_capabilities when the current working set lacks an action you need. Discovery results identify capabilities available on the next turn.`,
    tools,
    prepareStep: () => ({ activeTools: activeToolKeys() }),
    // One model call per stream lets the coordinator refresh the active-time
    // deadline after durable tool waits. The aggregate loop below owns the cap.
    stopWhen: isStepCount(1),
    maxRetries: 2,
    maxOutputTokens: 8_192,
    reasoning: context.reasoning_effort || undefined,
    telemetry: {
      isEnabled: true,
      recordInputs: false,
      recordOutputs: false,
      functionId: "misty.space-task-agent",
    },
    experimental_onStepStart: async ({ stepNumber }) => {
      if (stepNumber !== 0) throw new FatalError("unexpected_model_step: the pinned adapter exceeded one model call");
      await execution.checkpoint({
        node_id: `model:${modelTurn + 1}`,
        state: "running",
        phase: "thinking",
        progress: Math.min(85, 10 + modelTurn * 6),
      });
    },
    onStepEnd: async ({ finishReason, usage, text }) => {
      await execution.checkpoint({
        node_id: `model:${modelTurn + 1}`,
        state: "completed",
        phase: "working",
        progress: Math.min(90, 15 + modelTurn * 6),
        // The control plane projects this model-owned text into the public SSE
        // stream for interactive invocations. Tool-only steps normally have no
        // text, while the final step supplies Markdown as it becomes durable.
        output: { finish_reason: finishReason, usage, text_delta: text },
      });
    },
    onToolExecutionStart: ({ toolCall }) => toolExecutionOrder.start(toolCall.toolCallId, async () => {
      const canonicalName =
        controlPlaneNames[toolCall.toolName] ?? toolCall.toolName;
      toolCallSignatures.set(toolCall.toolCallId, {
        signature: toolFailureSignature(canonicalName, toolCall.input),
        canonicalName,
      });
      await execution.checkpoint({
        node_id: `tool:${toolCall.toolCallId}`,
        state: "running",
        phase: `using_${canonicalName.replaceAll(".", "_")}`,
        progress: 40,
        input:
          typeof toolCall.input === "object" && toolCall.input !== null
            ? (toolCall.input as Record<string, unknown>)
            : {},
      });
    }),
    onToolExecutionEnd: (event) => toolExecutionOrder.finish(event.toolCall.toolCallId, async () => {
      const { toolCall, durationMs, success } = event;
      const unconfirmedReason = success
        ? unconfirmedToolResultReason(event.output)
        : "";
      const confirmed = success && unconfirmedReason === "";
      const errorMessage = confirmed
        ? ""
        : unconfirmedReason || visibleErrorMessage(event.error);
      const canonicalName =
        controlPlaneNames[toolCall.toolName] ?? toolCall.toolName;
      const tracked = toolCallSignatures.get(toolCall.toolCallId);
      const signature =
        tracked?.signature ??
        toolFailureSignature(canonicalName, toolCall.input);
      if (!confirmed) {
        // Stop queued calls from the same model response before releasing the
        // current turn. The outer stop condition runs only after all calls.
        toolExecutionOrder.stop(errorMessage);
        failedToolCalls.set(signature, {
          callId: toolCall.toolCallId,
          toolName: tracked?.canonicalName ?? canonicalName,
          error: errorMessage,
        });
      } else {
        failedToolCalls.delete(signature);
        const completedName = tracked?.canonicalName ?? canonicalName;
        successfulToolCalls.add(completedName);
        const semantic = descriptors.find((item) => item.name === completedName)?.capability;
        if (semantic) successfulToolCalls.add(semantic);
      }
      await execution.checkpoint({
        node_id: `tool:${toolCall.toolCallId}`,
        state: confirmed ? "completed" : "failed",
        phase: confirmed ? "working" : "tool_failed",
        progress: confirmed ? 60 : 40,
        output: {
          tool: tracked?.canonicalName ?? canonicalName,
          duration_ms: durationMs,
          success: confirmed,
        },
        error_message: confirmed ? undefined : errorMessage,
      });
    }),
  });
  let result: Awaited<ReturnType<typeof agent.stream>> | undefined;
  let streamAborted = false;
  const legacyDeadline = Date.now() + 30 * 60_000;
  try {
    const shared = {
      providerOptions: {
        gateway: { models: fallbackModels(context.model_id) },
      },
      onAbort: async () => { streamAborted = true; },
      runtimeContext: { mistyRunId: input.mistyRunId },
    };
    const images = [
      ...(context.attachments ?? []),
      ...(context.capture ? [context.capture] : []),
    ];
    let messages: ModelMessage[] = images.length
      ? [
            {
              role: "user",
              content: [
                { type: "text", text: context.prompt },
                ...images.map(
                  (image) =>
                    ({
                      type: "image",
                      image: image.data_url,
                      mediaType: image.mime_type,
                    }) as const,
                ),
              ],
            },
          ]
      : [{ role: "user", content: context.prompt }];
    for (; modelTurn < 20; modelTurn++) {
      const budget = await fetchExecutionBudget(identity, modelTurn + 1);
      const current = await agent.stream({ messages, ...shared, timeout: modelTimeout(budget, Date.now(), legacyDeadline) });
      const steps = [...(result?.steps ?? []), ...current.steps];
      result = { ...current, steps, totalUsage: accumulateModelUsage(result?.totalUsage, current.totalUsage) };
      // WorkflowAgent returns the complete model transcript, including tool
      // results. Reinject our fixed system instructions once on the next call.
      messages = current.messages.filter((message) => message.role !== "system");
      if (streamAborted || toolExecutionOrder.stoppedReason || current.finishReason !== "tool-calls" || current.steps.length !== 1 || await stopOnRepeatedOrTerminalToolFailure({ steps })) break;
    }
    if (!result) throw new Error("empty_agent_response");
  } catch (error) {
    if (toolExecutionOrder.stoppedReason) {
      const failures = [...failedToolCalls.values()];
      const text = failures.length ? incompleteToolResultText(failures) : toolExecutionOrder.stoppedReason;
      await execution.complete({
        status: "incomplete", text, error_code: "tool_sequence_stopped",
        error_message: toolExecutionOrder.stoppedReason,
        usage: result?.totalUsage as unknown as Record<string, unknown> | undefined,
      });
      return { mistyRunId: input.mistyRunId, text, incomplete: true };
    }
    const failure = classifyRuntimeError(error);
    await execution.complete({
      status: "failed",
      text: "",
      error_code: failure.code,
      error_message: failure.message,
      usage: result?.totalUsage as unknown as Record<string, unknown> | undefined,
    });
    throw new FatalError(failure.message);
  }
  const unfinished = unfinishedModelResult(streamAborted, result.steps.length, result.finishReason);
  // Existing tool failures below carry more useful details, unless the stream
  // itself was interrupted or exhausted its model budget.
  if (unfinished && (streamAborted || unfinished.code === "agent_model_turn_limit")) {
    await execution.complete({ status: "incomplete", text: unfinished.message, usage: result.totalUsage as unknown as Record<string, unknown>, error_code: unfinished.code, error_message: unfinished.message });
    return { mistyRunId: input.mistyRunId, text: unfinished.message, steps: result.steps.length, incomplete: true };
  }
  const text = finalText(result.steps);
  const unresolvedToolFailures = [...failedToolCalls.values()];
  if (unresolvedToolFailures.length > 0) {
    const completion = classifyToolCompletion(
      unresolvedToolFailures.map((item) => item.toolName),
    );
    await execution.complete({
      status: completion.status,
      text: incompleteToolResultText(unresolvedToolFailures),
      usage: result.totalUsage as unknown as Record<string, unknown>,
      error_code: completion.error_code,
      error_message: completion.error_message,
    });
    return {
      mistyRunId: input.mistyRunId,
      text: incompleteToolResultText(unresolvedToolFailures),
      steps: result.steps.length,
      incomplete: true,
    };
  }
  const missingRequiredTools = missingRequiredToolCalls(
    context.required_tools ?? [],
    successfulToolCalls,
  );
  if (missingRequiredTools.length > 0) {
    const incompleteText = incompleteRequiredToolText(missingRequiredTools);
    await execution.complete({
      status: "incomplete",
      text: incompleteText,
      usage: result.totalUsage as unknown as Record<string, unknown>,
      error_code: "required_action_not_completed",
      error_message: `Required tool calls did not complete: ${missingRequiredTools.join(", ")}.`,
    });
    return {
      mistyRunId: input.mistyRunId,
      text: incompleteText,
      steps: result.steps.length,
      incomplete: true,
    };
  }
  if (unfinished) {
    await execution.complete({ status: "incomplete", text: unfinished.message, usage: result.totalUsage as unknown as Record<string, unknown>, error_code: unfinished.code, error_message: unfinished.message });
    return { mistyRunId: input.mistyRunId, text: unfinished.message, steps: result.steps.length, incomplete: true };
  }
  const completionText =
    text ||
    (context.required_tools?.length
      ? confirmedActionFallbackText(context.required_tools)
      : "");
  if (!completionText) {
    const incompleteText =
      "I couldn't fully complete that request because no final answer was produced.";
    await execution.complete({
      status: "incomplete",
      text: incompleteText,
      usage: result.totalUsage as unknown as Record<string, unknown>,
      error_code: "empty_agent_response",
      error_message:
        "The agent produced neither a final answer nor a required confirmed action.",
    });
    return {
      mistyRunId: input.mistyRunId,
      text: incompleteText,
      steps: result.steps.length,
      incomplete: true,
    };
  }
  await execution.complete({
    status: "success",
    text: completionText,
    usage: result.totalUsage as unknown as Record<string, unknown>,
  });
  return {
    mistyRunId: input.mistyRunId,
    text: completionText,
    steps: result.steps.length,
  };
}
