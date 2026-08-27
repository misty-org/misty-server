import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, jsonSchema, tool, type StopCondition } from "ai";
import { FatalError, getWorkflowMetadata, RetryableError } from "workflow";
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
import type {
  MCPRunAccess,
  RuntimeToolContext,
  SpaceTaskContext,
} from "../src/types.js";

export interface SpaceTaskWorkflowInput {
  mistyRunId: string;
  controlPlaneURL: string;
}

const toolContextSchema = z.object({
  mistyRunId: z.string().min(1),
  runtimeRunId: z.string().min(1),
  controlPlaneURL: z.string().url(),
});

const taskMutationFields = {
  title: z.string().max(500).optional(),
  notes: z.string().max(12_000).optional(),
  status: z.enum(["todo", "in_progress", "done", "canceled"]).optional(),
  priority: z.enum(["low", "medium", "high"]).optional(),
  dueAt: z
    .string()
    .datetime({ offset: true })
    .describe(
      "Due time with the creator's local UTC offset from authoritative context; do not use Z unless the creator requested UTC.",
    )
    .optional(),
  dueTimezone: z
    .string()
    .min(1)
    .max(80)
    .describe(
      "IANA timezone from authoritative context, such as America/Los_Angeles.",
    )
    .optional(),
  assigneeUserId: z.string().min(1).max(200).optional(),
};

export const taskCreateInputSchema = z.object({
  ...taskMutationFields,
  title: z.string().min(1).max(500),
});

const taskUpdateInputSchema = z.object({
  ...taskMutationFields,
  id: z.string().min(1),
});

const calendarMutationFields = {
  title: z.string().min(1).max(240).optional(),
  description: z.string().max(20_000).optional(),
  location: z.string().max(1_000).optional(),
  startsAt: z.string().datetime({ offset: true }).optional(),
  endsAt: z.string().datetime({ offset: true }).optional(),
  allDay: z.boolean().optional(),
  timezone: z.string().min(1).max(80).optional(),
  status: z.enum(["confirmed", "tentative", "canceled"]).optional(),
};

export const calendarCreateInputSchema = z.object({
  ...calendarMutationFields,
  title: z.string().min(1).max(240),
  startsAt: z.string().datetime({ offset: true }),
  endsAt: z.string().datetime({ offset: true }),
});

const calendarUpdateInputSchema = z.object({
  ...calendarMutationFields,
  id: z.string().min(1).max(200),
});

function rethrowStepError(error: unknown): never {
  if (error instanceof ControlPlaneError) {
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
      if (part.type !== "tool-error") continue;
      const signature = toolFailureSignature(part.toolName, part.input);
      const count = (counts.get(signature) ?? 0) + 1;
      counts.set(signature, count);
      const message = errorText(part.error).toLowerCase();
      if (
        message.includes("tool_unavailable") ||
        message.includes("permission_denied") ||
        message.includes("authorization_or_state_changed") ||
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
    const hookToken = `misty:${context.mistyRunId}:${callId}`;
    const deviceHookToken = `misty-device:${context.mistyRunId}:${callId}`;
    const response = await requestToolExecution(
      context,
      callId,
      name,
      input,
      hookToken,
      deviceHookToken,
      `${context.mistyRunId}:tool:${callId}`,
    );
    if (response.tool_error) {
      throw new Error(
        `${response.tool_error.code}: ${response.tool_error.message}`,
      );
    }
    if (response.device_wait) {
      const device = await agentDeviceHook.create({ token: deviceHookToken });
      if (!device.available)
        return { unavailable: true, reason: "device_unavailable" };
      const resumed = await requestToolExecution(
        context,
        callId,
        name,
        input,
        hookToken,
        deviceHookToken,
        `${context.mistyRunId}:tool:${callId}:device-resumed`,
      );
      if (resumed.tool_error) {
        throw new Error(
          `${resumed.tool_error.code}: ${resumed.tool_error.message}`,
        );
      }
      return resumed.result;
    }
    if (response.approval) {
      const decision = await agentToolApprovalHook.create({ token: hookToken });
      if (!decision.approved) {
        return {
          denied: true,
          reason: "creator_denied",
          approval_id: decision.approval_id,
        };
      }
      const resumed = await requestToolExecution(
        context,
        callId,
        name,
        input,
        hookToken,
        deviceHookToken,
        `${context.mistyRunId}:tool:${callId}:approved`,
      );
      if (resumed.tool_error) {
        throw new Error(
          `${resumed.tool_error.code}: ${resumed.tool_error.message}`,
        );
      }
      return resumed.result;
    }
    return response.result;
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
  tool_error?: { code: string; message: string };
}> {
  "use step";
  try {
    let access: MCPRunAccess;
    try {
      access = await controlPlaneRequest<MCPRunAccess>(
        context,
        "mcp-token",
        {},
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

const nativeControlPlaneToolNames = new Set([
  "context.get",
  "weather.current",
  "members.list",
  "members.resolve",
  "messages.search",
  "messages.send",
  "library.search",
  "library.read",
  "library.update",
  "library.promote_attachment",
  "notes.search",
  "notes.read",
  "notes.create",
  "notes.update",
  "drawings.list",
  "drawings.read",
  "drawings.create",
  "drawings.apply",
  "roadmaps.query",
  "roadmaps.read",
  "roadmaps.create",
  "roadmaps.update",
  "tasks.query",
  "tasks.update_assigned",
  "tasks.create",
  "tasks.update",
  "calendar.query",
  "calendar.create",
  "calendar.update",
  "agents.delegate",
  "agents.list",
  "agents.status",
  "memory.remember",
  "memory.forget",
  "browser.inspect",
  "browser.navigate",
  "browser.click",
  "browser.downloads.list",
  "task.activity.write",
  "attached_files.read",
]);

function remoteMCPToolKey(name: string, index: number): string {
  const slug = name
    .replace(/^mcp\./, "")
    .replace(/[^a-zA-Z0-9_-]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 42);
  return `remote_${index}_${slug || "tool"}`;
}

export function selectActiveRuntimeToolKeys(
  controlPlaneNames: Record<string, string>,
  allowedToolNames: Iterable<string>,
  catalog: { supported: boolean; advertisedToolNames: Iterable<string> },
): string[] {
  const allowed = new Set(allowedToolNames);
  const advertised = new Set(catalog.advertisedToolNames);
  return Object.keys(controlPlaneNames).filter((key) => {
    const canonicalName = controlPlaneNames[key];
    return (
      canonicalName !== undefined &&
      (allowed.has(canonicalName) || canonicalName.startsWith("mcp.")) &&
      (!catalog.supported || advertised.has(canonicalName))
    );
  });
}

async function queryTasks(
  input: {
    query?: string;
    status?: string;
    priority?: "low" | "medium" | "high";
    assigneeUserId?: string;
    from?: string;
    to?: string;
  },
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  return executeTool(options.context, options.toolCallId, "tasks.query", input);
}

async function executeNamedTool(
  name: string,
  input: Record<string, unknown>,
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
  return executeTool(options.context, options.toolCallId, name, input);
}

async function updateAssignedTask(
  input: { status?: "in_progress" | "done" | "canceled"; notes?: string },
  options: { context: RuntimeToolContext; toolCallId: string },
): Promise<unknown> {
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
    input?: Record<string, unknown>;
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
  const runtimeRunId = getWorkflowMetadata().workflowRunId;
  const identity: RuntimeIdentity = {
    mistyRunId: input.mistyRunId,
    runtimeRunId,
    controlPlaneURL: input.controlPlaneURL,
  };
  await activateRuntime(identity);
  const context = await fetchContext(identity);
  const mcpCatalog = await discoverRemoteMCPTools(identity);
  const advertisedMCPTools = new Set(
    mcpCatalog.tools.map((descriptor) => descriptor.name),
  );
  if (mcpCatalog.supported) {
    const missingAllowedTools = context.allowed_tools.filter(
      (name) => !advertisedMCPTools.has(name),
    );
    await checkpoint(identity, {
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
  const remoteMCPDescriptors = mcpCatalog.tools.filter(
    (descriptor) => !nativeControlPlaneToolNames.has(descriptor.name),
  );
  const sharedToolContext: RuntimeToolContext = identity;
  const nativeTools = {
    context_get: tool({
      description:
        "Get the authoritative current time, timezone, and Space identity for this run.",
      inputSchema: z.object({}),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("context.get", input, options),
    }),
    weather_current: tool({
      description:
        "Get live current weather for a city or postal location. Use this instead of guessing current conditions.",
      inputSchema: z.object({ location: z.string().min(1).max(240) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("weather.current", input, options),
    }),
    members_list: tool({
      description:
        "List members of the current Space with stable user IDs and roles.",
      inputSchema: z.object({}),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("members.list", input, options),
    }),
    members_resolve: tool({
      description:
        "Resolve a member name or email in the current Space. Never guess when multiple matches are returned.",
      inputSchema: z.object({ query: z.string().min(1).max(320) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("members.resolve", input, options),
    }),
    messages_search: tool({
      description:
        "Search messages visible to the creator in this run's Space.",
      inputSchema: z.object({ query: z.string().max(500).optional() }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("messages.search", input, options),
    }),
    messages_send: tool({
      description:
        "Send a Misty-authored message privately to one resolved member or to the shared Space chat. Use auto only when the member's wording makes the audience unambiguous; otherwise ask before calling.",
      inputSchema: z.object({
        message: z.string().min(1).max(12_000),
        audience: z.enum(["auto", "private", "space"]).optional(),
        recipientUserId: z.string().min(1).max(200).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("messages.send", input, options),
    }),
    library_search: tool({
      description:
        "Search Library items visible to the creator in this run's Space.",
      inputSchema: z.object({ query: z.string().max(500).optional() }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("library.search", input, options),
    }),
    library_read: tool({
      description:
        "Read metadata, caption, tags, and file facts for one visible Library item.",
      inputSchema: z.object({ id: z.string().min(1).max(200) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("library.read", input, options),
    }),
    library_update: tool({
      description:
        "Update the name, caption, tags, favorite, or hidden state of an identified Library item.",
      inputSchema: z.object({
        id: z.string().min(1).max(200),
        displayName: z.string().min(1).max(255).optional(),
        caption: z.string().max(4_000).optional(),
        tags: z.array(z.string()).max(100).optional(),
        favorite: z.boolean().optional(),
        hidden: z.boolean().optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("library.update", input, options),
    }),
    library_promote_attachment: tool({
      description:
        "Save an identified Space message attachment into the current Space Library.",
      inputSchema: z.object({ attachmentId: z.string().min(1).max(200) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("library.promote_attachment", input, options),
    }),
    notes_search: tool({
      description: "Search Notes visible in this run's Space.",
      inputSchema: z.object({
        query: z.string().max(500).optional(),
        limit: z.number().int().min(1).max(50).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("notes.search", input, options),
    }),
    notes_read: tool({
      description: "Read one Note visible in this run's Space.",
      inputSchema: z.object({ id: z.string().min(1).max(200) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("notes.read", input, options),
    }),
    notes_create: tool({
      description: "Create a native Note in this run's Space.",
      inputSchema: z.object({
        title: z.string().min(1).max(500),
        markdown: z.string().min(1).max(100_000),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("notes.create", input, options),
    }),
    notes_update: tool({
      description: "Replace the title or Markdown body of an identified Note.",
      inputSchema: z.object({
        id: z.string().min(1).max(200),
        title: z.string().min(1).max(500).optional(),
        markdown: z.string().max(100_000).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("notes.update", input, options),
    }),
    drawings_list: tool({
      description: "List collaborative Excalidraw drawings visible in this run's Space.",
      inputSchema: z.object({
        query: z.string().max(500).optional(),
        limit: z.number().int().min(1).max(100).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("drawings.list", input, options),
    }),
    drawings_read: tool({
      description: "Read one live Excalidraw scene and its current content hash.",
      inputSchema: z.object({
        drawing_id: z.string().min(1).max(200),
        include_deleted: z.boolean().optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("drawings.read", input, options),
    }),
    drawings_create: tool({
      description: "Create an empty collaborative Excalidraw drawing in this run's Space.",
      inputSchema: z.object({ title: z.string().max(200).optional() }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("drawings.create", input, options),
    }),
    drawings_apply: tool({
      description: "Create, edit, or delete elements in an identified Excalidraw drawing. Read first and pass its latest base_hash when editing an existing scene.",
      inputSchema: z.object({
        drawing_id: z.string().min(1).max(200),
        base_hash: z.string().length(64).optional(),
        mode: z.enum(["merge", "replace"]).optional(),
        elements: z.array(z.object({
          id: z.string().min(1).max(128),
          type: z.enum(["rectangle", "diamond", "ellipse", "text", "line", "arrow", "freedraw", "image", "frame", "magicframe", "iframe", "embeddable"]).optional(),
        }).catchall(z.unknown())).max(500).optional(),
        delete_element_ids: z.array(z.string().min(1).max(128)).max(500).optional(),
        scene: z.object({ viewBackgroundColor: z.string().max(100).optional() }).catchall(z.unknown()).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("drawings.apply", input, options),
    }),
    roadmaps_query: tool({
      description: "List or search roadmaps visible in this run's Space.",
      inputSchema: z.object({ query: z.string().max(500).optional() }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("roadmaps.query", input, options),
    }),
    roadmaps_read: tool({
      description:
        "Read one roadmap including its milestones, goals, nodes, and progress.",
      inputSchema: z.object({ id: z.string().min(1).max(200) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("roadmaps.read", input, options),
    }),
    roadmaps_create: tool({
      description: "Create a roadmap in this run's Space.",
      inputSchema: z.object({
        name: z.string().min(1).max(160),
        description: z.string().max(5_000).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("roadmaps.create", input, options),
    }),
    roadmaps_update: tool({
      description: "Update an explicitly identified roadmap.",
      inputSchema: z.object({
        id: z.string().min(1).max(200),
        name: z.string().min(1).max(160).optional(),
        description: z.string().max(5_000).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("roadmaps.update", input, options),
    }),
    tasks_query: tool({
      description:
        "Query Tasks visible in this run's Space. Use from and to from authoritative timezone context for date-bounded questions such as due today or overdue.",
      inputSchema: z.object({
        query: z.string().max(500).optional(),
        status: z.string().max(40).optional(),
        priority: z.enum(["low", "medium", "high"]).optional(),
        assigneeUserId: z.string().max(200).optional(),
        from: z.string().datetime({ offset: true }).optional(),
        to: z.string().datetime({ offset: true }).optional(),
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
    tasks_create: tool({
      description:
        "Create a task in this run's Space. Use todo for a new task. For due times, preserve the creator's wall-clock time using the UTC offset and IANA timezone from authoritative context.",
      inputSchema: taskCreateInputSchema,
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("tasks.create", input, options),
    }),
    tasks_update: tool({
      description: "Update an explicitly identified task in this run's Space.",
      inputSchema: taskUpdateInputSchema,
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("tasks.update", input, options),
    }),
    calendar_query: tool({
      description: "Query the current Space calendar.",
      inputSchema: z.object({
        from: z.string().datetime({ offset: true }).optional(),
        to: z.string().datetime({ offset: true }).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("calendar.query", input, options),
    }),
    calendar_create: tool({
      description:
        "Create a native event in this run's Space calendar. Use authoritative context for the current date and timezone.",
      inputSchema: calendarCreateInputSchema,
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("calendar.create", input, options),
    }),
    calendar_update: tool({
      description:
        "Update an explicitly identified native event in this run's Space calendar.",
      inputSchema: calendarUpdateInputSchema,
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("calendar.update", input, options),
    }),
    agents_delegate: tool({
      description: context.managed_misty
        ? "Delegate bounded independent work to a hidden background worker. Misty remains responsible for the result."
        : "Delegate bounded work to another companion Agent owned by the same creator in this Space.",
      inputSchema: context.managed_misty
        ? z.object({ prompt: z.string().min(1).max(16_000) })
        : z.object({
            prompt: z.string().min(1).max(16_000),
            agent_id: z.string().optional(),
            agent_name: z.string().optional(),
          }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("agents.delegate", input, options),
    }),
    agents_list: tool({
      description:
        "List the creator's enabled companion Agents available in this Space.",
      inputSchema: z.object({}),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("agents.list", input, options),
    }),
    agents_status: tool({
      description:
        "Check whether a creator-owned companion Agent is available or busy.",
      inputSchema: z.object({
        agentId: z.string().max(200).optional(),
        agentName: z.string().max(200).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("agents.status", input, options),
    }),
    memory_remember: tool({
      description:
        "Remember a concise fact, preference, or standing instruction only when the user explicitly asks. Never store credentials, secrets, financial identifiers, health records, or inferred sensitive traits.",
      inputSchema: z.object({
        content: z.string().min(1).max(1_000),
        kind: z.enum(["fact", "preference", "instruction"]),
        scope: z.enum(["personal", "space"]),
        reason: z.string().max(500).optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("memory.remember", input, options),
    }),
    memory_forget: tool({
      description:
        "Forget one remembered item only when the user explicitly asks. Use its exact ID from remembered context.",
      inputSchema: z.object({ memoryId: z.string().min(1).max(200) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("memory.forget", input, options),
    }),
    browser_inspect: tool({
      description:
        "Inspect untrusted page text and actionable element references in an attached browser tab.",
      inputSchema: z.object({ scopeId: z.string().min(8).max(256) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("browser.inspect", input, options),
    }),
    browser_navigate: tool({
      description: "Navigate an attached browser tab to an HTTP or HTTPS URL.",
      inputSchema: z.object({
        scopeId: z.string().min(8).max(256),
        url: z.string().url().max(4096),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("browser.navigate", input, options),
    }),
    browser_click: tool({
      description:
        "Click an element reference from the latest inspection of an attached browser tab.",
      inputSchema: z.object({
        scopeId: z.string().min(8).max(256),
        elementRef: z.string().max(128),
        expectDownload: z.boolean().optional(),
      }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("browser.click", input, options),
    }),
    browser_downloads_list: tool({
      description:
        "List recent downloads associated with an attached browser tab.",
      inputSchema: z.object({ scopeId: z.string().min(8).max(256) }),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("browser.downloads.list", input, options),
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
  const remoteTools = Object.fromEntries(
    remoteMCPDescriptors.map((descriptor, index) => [
      remoteMCPToolKey(descriptor.name, index),
      tool({
        description: descriptor.description,
        inputSchema: jsonSchema(
          descriptor.inputSchema as Parameters<typeof jsonSchema>[0],
        ),
        contextSchema: toolContextSchema,
        execute: (input, options) =>
          executeNamedTool(
            descriptor.name,
            input as Record<string, unknown>,
            options,
          ),
      }),
    ]),
  );
  const tools = { ...nativeTools, ...remoteTools };
  const controlPlaneNames: Record<string, string> = {
    context_get: "context.get",
    weather_current: "weather.current",
    members_list: "members.list",
    members_resolve: "members.resolve",
    messages_search: "messages.search",
    messages_send: "messages.send",
    library_search: "library.search",
    library_read: "library.read",
    library_update: "library.update",
    library_promote_attachment: "library.promote_attachment",
    notes_search: "notes.search",
    notes_read: "notes.read",
    notes_create: "notes.create",
    notes_update: "notes.update",
    drawings_list: "drawings.list",
    drawings_read: "drawings.read",
    drawings_create: "drawings.create",
    drawings_apply: "drawings.apply",
    roadmaps_query: "roadmaps.query",
    roadmaps_read: "roadmaps.read",
    roadmaps_create: "roadmaps.create",
    roadmaps_update: "roadmaps.update",
    tasks_query: "tasks.query",
    tasks_update_assigned: "tasks.update_assigned",
    tasks_create: "tasks.create",
    tasks_update: "tasks.update",
    calendar_query: "calendar.query",
    calendar_create: "calendar.create",
    calendar_update: "calendar.update",
    agents_delegate: "agents.delegate",
    agents_list: "agents.list",
    agents_status: "agents.status",
    memory_remember: "memory.remember",
    memory_forget: "memory.forget",
    browser_inspect: "browser.inspect",
    browser_navigate: "browser.navigate",
    browser_click: "browser.click",
    browser_downloads_list: "browser.downloads.list",
    task_activity_write: "task.activity.write",
    attached_files_read: "attached_files.read",
  };
  remoteMCPDescriptors.forEach((descriptor, index) => {
    controlPlaneNames[remoteMCPToolKey(descriptor.name, index)] =
      descriptor.name;
  });
  const activeTools = selectActiveRuntimeToolKeys(
    controlPlaneNames,
    context.allowed_tools,
    {
      supported: mcpCatalog.supported,
      advertisedToolNames: advertisedMCPTools,
    },
  ) as Array<keyof typeof tools>;
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
  const agent = new WorkflowAgent({
    id: "misty-space-task-agent",
    model: context.model_id,
    instructions: context.system,
    tools,
    toolsContext: {
      context_get: sharedToolContext,
      weather_current: sharedToolContext,
      members_list: sharedToolContext,
      members_resolve: sharedToolContext,
      tasks_query: sharedToolContext,
      messages_search: sharedToolContext,
      messages_send: sharedToolContext,
      library_search: sharedToolContext,
      library_read: sharedToolContext,
      library_update: sharedToolContext,
      library_promote_attachment: sharedToolContext,
      notes_search: sharedToolContext,
      notes_read: sharedToolContext,
      notes_create: sharedToolContext,
      notes_update: sharedToolContext,
      drawings_list: sharedToolContext,
      drawings_read: sharedToolContext,
      drawings_create: sharedToolContext,
      drawings_apply: sharedToolContext,
      roadmaps_query: sharedToolContext,
      roadmaps_read: sharedToolContext,
      roadmaps_create: sharedToolContext,
      roadmaps_update: sharedToolContext,
      tasks_update_assigned: sharedToolContext,
      tasks_create: sharedToolContext,
      tasks_update: sharedToolContext,
      calendar_query: sharedToolContext,
      calendar_create: sharedToolContext,
      calendar_update: sharedToolContext,
      agents_delegate: sharedToolContext,
      agents_list: sharedToolContext,
      agents_status: sharedToolContext,
      memory_remember: sharedToolContext,
      memory_forget: sharedToolContext,
      browser_inspect: sharedToolContext,
      browser_navigate: sharedToolContext,
      browser_click: sharedToolContext,
      browser_downloads_list: sharedToolContext,
      task_activity_write: sharedToolContext,
      attached_files_read: sharedToolContext,
      ...Object.fromEntries(
        Object.keys(remoteTools).map((name) => [name, sharedToolContext]),
      ),
    },
    stopWhen: [isStepCount(12), stopOnRepeatedOrTerminalToolFailure],
    maxRetries: 2,
    maxOutputTokens: 2_200,
    reasoning: context.reasoning_effort || undefined,
    telemetry: {
      isEnabled: true,
      recordInputs: false,
      recordOutputs: false,
      functionId: "misty.space-task-agent",
    },
    experimental_onStepStart: async ({ stepNumber }) => {
      await checkpoint(identity, {
        node_id: `model:${stepNumber + 1}`,
        state: "running",
        phase: "thinking",
        progress: Math.min(85, 10 + stepNumber * 6),
      });
    },
    onStepEnd: async ({ stepNumber, finishReason, usage, text }) => {
      await checkpoint(identity, {
        node_id: `model:${stepNumber + 1}`,
        state: "completed",
        phase: "working",
        progress: Math.min(90, 15 + stepNumber * 6),
        // The control plane projects this model-owned text into the public SSE
        // stream for interactive invocations. Tool-only steps normally have no
        // text, while the final step supplies Markdown as it becomes durable.
        output: { finish_reason: finishReason, usage, text_delta: text },
      });
    },
    onToolExecutionStart: async ({ toolCall }) => {
      const canonicalName =
        controlPlaneNames[toolCall.toolName] ?? toolCall.toolName;
      toolCallSignatures.set(toolCall.toolCallId, {
        signature: toolFailureSignature(canonicalName, toolCall.input),
        canonicalName,
      });
      await checkpoint(identity, {
        node_id: `tool:${toolCall.toolCallId}`,
        state: "running",
        phase: `using_${canonicalName.replaceAll(".", "_")}`,
        progress: 40,
        input:
          typeof toolCall.input === "object" && toolCall.input !== null
            ? (toolCall.input as Record<string, unknown>)
            : {},
      });
    },
    onToolExecutionEnd: async (event) => {
      const { toolCall, durationMs, success } = event;
      const errorMessage = success ? "" : visibleErrorMessage(event.error);
      const canonicalName =
        controlPlaneNames[toolCall.toolName] ?? toolCall.toolName;
      const tracked = toolCallSignatures.get(toolCall.toolCallId);
      const signature =
        tracked?.signature ??
        toolFailureSignature(canonicalName, toolCall.input);
      if (!success) {
        failedToolCalls.set(signature, {
          callId: toolCall.toolCallId,
          toolName: tracked?.canonicalName ?? canonicalName,
          error: errorMessage,
        });
      } else {
        failedToolCalls.delete(signature);
      }
      await checkpoint(identity, {
        node_id: `tool:${toolCall.toolCallId}`,
        state: success ? "completed" : "failed",
        phase: success ? "working" : "tool_failed",
        progress: success ? 60 : 40,
        output: {
          tool: tracked?.canonicalName ?? canonicalName,
          duration_ms: durationMs,
          success,
        },
        error_message: success ? undefined : errorMessage,
      });
    },
  });
  let result;
  try {
    const shared = {
      activeTools,
      providerOptions: {
        gateway: { models: fallbackModels(context.model_id) },
      },
      // WorkflowAgent applies this inside its durable model step. The workflow
      // coordinator does not expose AbortSignal and must not construct one.
      timeout: 30 * 60_000,
      runtimeContext: { mistyRunId: input.mistyRunId },
    };
    const images = [
      ...(context.attachments ?? []),
      ...(context.capture ? [context.capture] : []),
    ];
    result = images.length
      ? await agent.stream({
          messages: [
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
          ],
          ...shared,
        })
      : await agent.stream({ prompt: context.prompt, ...shared });
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
  const unresolvedToolFailures = [...failedToolCalls.values()];
  if (unresolvedToolFailures.length > 0) {
    const completion = classifyToolCompletion(
      unresolvedToolFailures.map((item) => item.toolName),
    );
    await complete(identity, {
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
  await complete(identity, {
    status: "success",
    text,
    usage: result.totalUsage as unknown as Record<string, unknown>,
  });
  return { mistyRunId: input.mistyRunId, text, steps: result.steps.length };
}
