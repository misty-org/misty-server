import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, tool } from "ai";
import { FatalError, getWorkflowMetadata } from "workflow";
import { z } from "zod";
import {
  controlPlaneRequest,
  type RuntimeIdentity,
} from "../src/control-plane.js";
import { ControlPlaneError } from "../src/control-plane-error.js";
import { agentToolApprovalHook } from "../src/approval.js";
import { agentDeviceHook } from "../src/device.js";
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
  if (error instanceof ControlPlaneError && !error.transient) {
    throw new FatalError(error.message);
  }
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
    return { code: "agent_runtime_timeout", message };
  }
  if (error instanceof ControlPlaneError && !error.transient) {
    return { code: "authorization_or_state_changed", message };
  }
  return { code: "agent_runtime_failed", message };
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
      const label = toolName.replaceAll("_", " ");
      const reason = error.trim().slice(0, 300) || "the action was rejected";
      return `${label}: ${reason}`;
    })
    .join("; ");
  return `I couldn't fully complete that request. ${details || "A required action failed."}`;
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
  } catch (error) {
    const recoverable = recoverableToolError(error);
    if (recoverable) return { tool_error: recoverable };
    rethrowStepError(error);
  }
}

async function queryTasks(
  input: { query?: string; status?: string },
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

function providerQueryTool(
  provider: "discord" | "google" | "notion" | "slack",
) {
  return tool({
    description: `Query the ${provider} connection shared with this Space without exposing credentials.`,
    inputSchema: z.object({
      query: z.string().max(2_000).optional(),
      limit: z.number().int().min(1).max(50).optional(),
      resource: z.string().max(500).optional(),
    }),
    contextSchema: toolContextSchema,
    execute: (input, options) =>
      executeNamedTool(`provider.${provider}.query`, input, options),
  });
}

function providerWriteTool(provider: "discord" | "slack") {
  return tool({
    description: `Write through the ${provider} connection shared with this Space. This is consequential.`,
    inputSchema: z.object({
      destination: z.string().max(500).optional(),
      resource: z.string().max(500).optional(),
      payload: z.record(z.string(), z.unknown()).optional(),
      mode: z.enum(["draft", "send"]).optional(),
    }),
    contextSchema: toolContextSchema,
    execute: (input, options) =>
      executeNamedTool(`provider.${provider}.write`, input, options),
  });
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
  const message = error instanceof Error ? error.message : String(error);
  return message.trim().slice(0, 500) || "Tool execution failed";
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
    context_get: tool({
      description:
        "Get the authoritative current time, timezone, and Space identity for this run.",
      inputSchema: z.object({}),
      contextSchema: toolContextSchema,
      execute: (input, options) =>
        executeNamedTool("context.get", input, options),
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
        "Send an agent-authored message to the main Space chat. This is consequential.",
      inputSchema: z.object({ message: z.string().min(1).max(12_000) }),
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
    provider_discord_query: providerQueryTool("discord"),
    provider_discord_write: providerWriteTool("discord"),
    provider_google_query: providerQueryTool("google"),
    provider_notion_query: providerQueryTool("notion"),
    provider_slack_query: providerQueryTool("slack"),
    provider_slack_write: providerWriteTool("slack"),
    agents_delegate: tool({
      description:
        "Delegate bounded work to another companion Agent owned by the same creator in this Space.",
      inputSchema: z.object({
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
  const controlPlaneNames: Record<keyof typeof tools, string> = {
    context_get: "context.get",
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
    provider_discord_query: "provider.discord.query",
    provider_discord_write: "provider.discord.write",
    provider_google_query: "provider.google.query",
    provider_notion_query: "provider.notion.query",
    provider_slack_query: "provider.slack.query",
    provider_slack_write: "provider.slack.write",
    agents_delegate: "agents.delegate",
    agents_list: "agents.list",
    agents_status: "agents.status",
    browser_inspect: "browser.inspect",
    browser_navigate: "browser.navigate",
    browser_click: "browser.click",
    browser_downloads_list: "browser.downloads.list",
    task_activity_write: "task.activity.write",
    attached_files_read: "attached_files.read",
  };
  const allowed = new Set(context.allowed_tools);
  const activeTools = (
    Object.keys(controlPlaneNames) as Array<keyof typeof tools>
  ).filter((name) => allowed.has(controlPlaneNames[name]));
  const failedToolCalls: Array<{
    callId: string;
    toolName: string;
    error: string;
  }> = [];
  const agent = new WorkflowAgent({
    id: "misty-space-task-agent",
    model: context.model_id,
    instructions: context.system,
    tools,
    toolsContext: {
      context_get: sharedToolContext,
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
      provider_discord_query: sharedToolContext,
      provider_discord_write: sharedToolContext,
      provider_google_query: sharedToolContext,
      provider_notion_query: sharedToolContext,
      provider_slack_query: sharedToolContext,
      provider_slack_write: sharedToolContext,
      agents_delegate: sharedToolContext,
      agents_list: sharedToolContext,
      agents_status: sharedToolContext,
      browser_inspect: sharedToolContext,
      browser_navigate: sharedToolContext,
      browser_click: sharedToolContext,
      browser_downloads_list: sharedToolContext,
      task_activity_write: sharedToolContext,
      attached_files_read: sharedToolContext,
    },
    stopWhen: isStepCount(30),
    maxRetries: 3,
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
        input:
          typeof toolCall.input === "object" && toolCall.input !== null
            ? (toolCall.input as Record<string, unknown>)
            : {},
      });
    },
    onToolExecutionEnd: async (event) => {
      const { toolCall, durationMs, success } = event;
      const errorMessage = success ? "" : visibleErrorMessage(event.error);
      if (!success) {
        const existing = failedToolCalls.find(
          (item) => item.callId === toolCall.toolCallId,
        );
        if (existing) existing.error = errorMessage;
        else
          failedToolCalls.push({
            callId: toolCall.toolCallId,
            toolName: toolCall.toolName,
            error: errorMessage,
          });
      }
      await checkpoint(identity, {
        node_id: `tool:${toolCall.toolCallId}`,
        state: success ? "completed" : "failed",
        phase: success ? "working" : "tool_failed",
        progress: success ? 60 : 40,
        output: {
          tool: toolCall.toolName,
          duration_ms: durationMs,
          success,
        },
        error_message: success ? undefined : errorMessage,
      });
    },
  });
  let result;
  try {
    result = await agent.stream({
      prompt: context.prompt,
      activeTools,
      // WorkflowAgent applies this inside its durable model step. The workflow
      // coordinator does not expose AbortSignal and must not construct one.
      timeout: 30 * 60_000,
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
  if (failedToolCalls.length > 0) {
    const completion = classifyToolCompletion(
      failedToolCalls.map((item) => item.toolName),
    );
    await complete(identity, {
      status: completion.status,
      text: incompleteToolResultText(failedToolCalls),
      usage: result.totalUsage as unknown as Record<string, unknown>,
      error_code: completion.error_code,
      error_message: completion.error_message,
    });
    return {
      mistyRunId: input.mistyRunId,
      text: incompleteToolResultText(failedToolCalls),
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
