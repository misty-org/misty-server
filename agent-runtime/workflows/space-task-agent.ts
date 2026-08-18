import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, tool } from "ai";
import { FatalError, getWorkflowMetadata } from "workflow";
import { z } from "zod";
import {
  controlPlaneRequest,
  type RuntimeIdentity,
} from "../src/control-plane.js";
import { ControlPlaneError } from "../src/control-plane-error.js";
import type { RuntimeToolContext, SpaceTaskContext } from "../src/types.js";

export interface SpaceTaskWorkflowInput {
  mistyRunId: string;
  controlPlaneURL: string;
}

const toolContextSchema = z.object({
  mistyRunId: z.string().min(1),
  runtimeRunId: z.string().min(1),
  controlPlaneURL: z.string().url(),
});

function rethrowStepError(error: unknown): never {
  if (error instanceof ControlPlaneError && !error.transient) {
    throw new FatalError(error.message);
  }
  throw error;
}

export function classifyRuntimeError(error: unknown): {
  code: string;
  message: string;
} {
  const message =
    error instanceof Error ? error.message : "Agent workflow failed";
  const normalized =
    `${error instanceof Error ? error.name : ""} ${message}`.toLowerCase();
  if (
    normalized.includes("timeout") ||
    normalized.includes("timed out") ||
    normalized.includes("aborted")
  ) {
    return { code: "agent_runtime_timeout", message };
  }
  if (error instanceof ControlPlaneError && !error.transient) {
    return { code: "authorization_or_state_changed", message };
  }
  return { code: "agent_runtime_failed", message };
}

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
      {},
      `${identity.mistyRunId}:context`,
    );
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
    const response = await controlPlaneRequest<{ result: unknown }>(
      context,
      "tools",
      { call_id: callId, name, arguments: input },
      `${context.mistyRunId}:tool:${callId}`,
    );
    return response.result;
  } catch (error) {
    rethrowStepError(error);
  }
}

async function queryTasks(
  input: { query?: string; status?: string },
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  "use step";
  return executeTool(options.context, options.toolCallId, "tasks.query", input);
}

async function updateAssignedTask(
  input: { status?: "in_progress" | "done" | "canceled"; notes?: string },
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  "use step";
  return executeTool(
    options.context,
    options.toolCallId,
    "tasks.update_assigned",
    input,
  );
}

async function writeTaskActivity(
  input: { kind: "progress" | "result"; message: string },
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  "use step";
  return executeTool(
    options.context,
    options.toolCallId,
    "task.activity.write",
    input,
  );
}

async function readAttachedFiles(
  input: Record<string, never>,
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  "use step";
  return executeTool(
    options.context,
    options.toolCallId,
    "attached_files.read",
    input,
  );
}

async function checkpoint(
  identity: RuntimeIdentity,
  event: {
    node_id: string;
    state: "running" | "completed" | "failed";
    phase: string;
    progress: number;
    output?: Record<string, unknown>;
    error_message?: string;
  },
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
  result: {
    status: "success" | "incomplete" | "failed";
    text: string;
    usage?: Record<string, unknown>;
    error_code?: string;
    error_message?: string;
  },
): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(
      identity,
      "complete",
      result,
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

export async function runSpaceTaskAgent(input: SpaceTaskWorkflowInput) {
  "use workflow";
  const runtimeRunId = getWorkflowMetadata().workflowRunId;
  const identity: RuntimeIdentity = {
    mistyRunId: input.mistyRunId,
    runtimeRunId,
    controlPlaneURL: input.controlPlaneURL,
  };
  await activateRuntime(identity);
  const context = await fetchContext(identity);
  const sharedToolContext: RuntimeToolContext = identity;
  const tools = {
    tasks_query: tool({
      description: "Query Tasks visible in the assigned Task's Space.",
      inputSchema: z.object({
        query: z.string().max(500).optional(),
        status: z.string().max(40).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: queryTasks,
    }),
    tasks_update_assigned: tool({
      description:
        "Update only the assigned Task. Explicitly set status to done only when all requested work is complete.",
      inputSchema: z.object({
        status: z.enum(["in_progress", "done", "canceled"]).optional(),
        notes: z.string().max(12_000).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: updateAssignedTask,
    }),
    task_activity_write: tool({
      description:
        "Add a concise progress update or detailed result to the assigned Task's activity log.",
      inputSchema: z.object({
        kind: z.enum(["progress", "result"]),
        message: z.string().min(1).max(12_000),
      }),
      contextSchema: toolContextSchema,
      execute: writeTaskActivity,
    }),
    attached_files_read: tool({
      description:
        "Read only files explicitly attached to the assigned Task. This cannot browse arbitrary Space or Library content.",
      inputSchema: z.object({}),
      contextSchema: toolContextSchema,
      execute: readAttachedFiles,
    }),
  };
  const agent = new WorkflowAgent({
    id: "misty-space-task-agent",
    model: context.model_id,
    instructions: context.system,
    tools,
    toolsContext: {
      tasks_query: sharedToolContext,
      tasks_update_assigned: sharedToolContext,
      task_activity_write: sharedToolContext,
      attached_files_read: sharedToolContext,
    },
    stopWhen: isStepCount(12),
    maxRetries: 2,
    maxOutputTokens: 2_200,
    reasoning: context.reasoning_effort || undefined,
    telemetry: {
      isEnabled: true,
      recordInputs: false,
      recordOutputs: false,
      functionId: "misty.space-task-agent",
    },
    prepareStep: async () => ({ abortSignal: AbortSignal.timeout(90_000) }),
    experimental_onStepStart: async ({ stepNumber }) => {
      await checkpoint(identity, {
        node_id: `model:${stepNumber + 1}`,
        state: "running",
        phase: "thinking",
        progress: Math.min(85, 10 + stepNumber * 6),
      });
    },
    onStepEnd: async ({ stepNumber, finishReason, usage }) => {
      await checkpoint(identity, {
        node_id: `model:${stepNumber + 1}`,
        state: "completed",
        phase: "working",
        progress: Math.min(90, 15 + stepNumber * 6),
        output: { finish_reason: finishReason, usage },
      });
    },
    onToolExecutionStart: async ({ toolCall }) => {
      await checkpoint(identity, {
        node_id: `tool:${toolCall.toolCallId}`,
        state: "running",
        phase: `using_${toolCall.toolName}`,
        progress: 40,
      });
    },
    onToolExecutionEnd: async ({ toolCall, durationMs, success }) => {
      await checkpoint(identity, {
        node_id: `tool:${toolCall.toolCallId}`,
        state: success ? "completed" : "failed",
        phase: success ? "working" : "tool_failed",
        progress: success ? 60 : 40,
        output: { tool: toolCall.toolName, duration_ms: durationMs, success },
        error_message: success ? undefined : "Tool execution failed",
      });
    },
  });
  let result;
  try {
    result = await agent.stream({
      prompt: context.prompt,
      timeout: 10 * 60_000,
      runtimeContext: { mistyRunId: input.mistyRunId },
    });
  } catch (error) {
    const failure = classifyRuntimeError(error);
    await complete(identity, {
      status: "failed",
      text: "",
      error_code: failure.code,
      error_message: failure.message,
    });
    throw new FatalError(failure.message);
  }
  const text = finalText(result.steps);
  await complete(identity, {
    status: "success",
    text,
    usage: result.totalUsage as unknown as Record<string, unknown>,
  });
  return { mistyRunId: input.mistyRunId, text, steps: result.steps.length };
}
