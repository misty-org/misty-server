import { WorkflowAgent } from "@ai-sdk/workflow";
import { isStepCount, tool, type ModelMessage, type ToolSet } from "ai";
import { FatalError, getWorkflowMetadata } from "workflow";
import { browserReinspectionInstruction } from "../src/browser-reinspection.js";
import { summarizeOlderSteps } from "../src/compaction-step.js";
import { applyCompaction, CLEAR_AT, clearOldToolResults, COMPACT_AT, contextWindow, estimateInputTokens, messageBytes, planCompaction } from "../src/context-compaction.js";
import type { RuntimeIdentity } from "../src/control-plane.js";
import { activateRuntime, checkpoint, complete, fetchContext, fetchExecutionBudget, takeSteering } from "../src/control-plane-steps.js";
import { MISTY_HARNESS_VERSION, type HarnessCompletion, type HarnessExecution } from "../src/harness.js";
import { InstanceModel } from "../src/instance-model.js";
import { discoverRemoteMCPTools } from "../src/mcp-runtime.js";
import { accumulateModelUsage, modelTimeout, modelTurnLimit } from "../src/model-budget.js";
import { initialMessages } from "../src/model-input.js";
import { catalogTools, modelCatalog } from "../src/model-tools.js";
import { classifyRuntimeError } from "../src/runtime-errors.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { finalTaskCompletion, taskCompletionInstructions, taskCompletionOutcome, taskCompletionSchema, taskCompletionText, taskCompletionTool } from "../src/task-completion.js";
import { executeTool } from "../src/tool-calls.js";
import { rejectedWithoutEffect } from "../src/tool-execution.js";
import { toolMonitor } from "../src/tool-monitor.js";
import { executionInstructions, finalText, incompleteToolResultText, unfinishedModelResult } from "../src/tool-outcomes.js";
import type { SpaceTaskContext } from "../src/types.js";

export interface SpaceTaskWorkflowInput {
  adapterVersion?: typeof MISTY_HARNESS_VERSION;
  mistyRunId: string;
  controlPlaneURL: string;
}

/**
 * One durable agent run. The model sees every tool in the run's catalog under
 * its real name; Misty's gateway authorizes, journals and executes each call.
 * The model reports the outcome with misty_finish_task.
 */
export async function runSpaceTaskAgent(input: SpaceTaskWorkflowInput) {
  "use workflow";
  if (input.adapterVersion && input.adapterVersion !== MISTY_HARNESS_VERSION) throw new FatalError("harness_version_unavailable");
  const identity: RuntimeIdentity = { mistyRunId: input.mistyRunId, runtimeRunId: getWorkflowMetadata().workflowRunId, controlPlaneURL: input.controlPlaneURL };
  // Calls Misty reported as rejected without effect; the model may correct them.
  const rejected = new Set<string>();
  const execution: HarnessExecution = {
    executeCapability: async (callId, name, value) => {
      try {
        return await executeTool(identity, callId, name, value);
      } catch (error) {
        if (rejectedWithoutEffect(error)) rejected.add(callId);
        throw error;
      }
    },
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
  if (!mcpCatalog.supported) {
    await execution.complete({
      status: "failed", text: "", error_code: "capability_registry_unavailable",
      error_message: "This runtime requires the shared capability registry. Keep the pinned previous runtime available while upgrading.",
    });
    throw new FatalError("Misty's capability registry is unavailable.");
  }
  const catalog = modelCatalog(mcpCatalog.tools, [taskCompletionTool]);
  const advertised = new Set(mcpCatalog.tools.map((descriptor) => descriptor.name));
  await execution.checkpoint({
    node_id: "mcp:catalog", state: "completed", phase: "tools_ready", progress: 8,
    output: {
      advertised_tool_count: advertised.size,
      missing_allowed_tools: context.allowed_tools.filter((name) => !advertised.has(name)),
      omitted_tools: catalog.omitted,
    },
  });
  if (context.routine_execution !== undefined) throw new FatalError("Retired automation execution cannot be resumed.");

  const explaining = context.companion_explanation === true;
  const order = serialToolLifecycle();
  const monitor = toolMonitor({ catalog, order, checkpoint: execution.checkpoint, rejected });
  const guard = (callId: string) => {
    const declined = order.declined(callId);
    if (declined) throw new Error(declined);
  };
  const tools: ToolSet = explaining ? {} : {
    ...catalogTools(catalog, async (callId, name, value) => {
      guard(callId);
      return await execution.executeCapability(callId, name, value);
    }),
    [taskCompletionTool]: tool({
      description: "Report the final task outcome and user-facing answer. Call alone after completing all possible work, or when a real blocker prevents further progress. This records a report; it grants no capabilities and performs no external action.",
      inputSchema: taskCompletionSchema,
      execute: async (report, options) => {
        guard(options.toolCallId);
        return report;
      },
    }),
  };
  const toolNames = Object.keys(tools);
  const instructions = explaining
    ? context.system
    : [context.system, "", executionInstructions, taskCompletionInstructions].join("\n");
  let modelTurn = 0;
  // The agent swallows errors thrown from its step hooks, so a failed settlement
  // checkpoint is held here and rethrown once the call returns.
  let hookFailure: unknown;
  const agent = new WorkflowAgent({
    id: "misty-space-task-agent",
    model: new InstanceModel(context.model_id, identity),
    instructions,
    tools,
    prepareStep: () => ({ activeTools: explaining ? [] : monitor.activeTools(toolNames) }),
    // One model call per stream lets the coordinator refresh the active-time
    // deadline after durable tool waits. The aggregate loop below owns the cap.
    stopWhen: isStepCount(1),
    maxRetries: 2,
    // Match the Go admission ceiling (agents.MaxModelOutputTokens).
    maxOutputTokens: 2_200,
    reasoning: context.reasoning_effort === "max" ? undefined : context.reasoning_effort || undefined,
    telemetry: { isEnabled: true, recordInputs: false, recordOutputs: false, functionId: "misty.space-task-agent" },
    experimental_onStepStart: async ({ stepNumber }) => {
      if (stepNumber !== 0) throw new FatalError("unexpected_model_step: the pinned adapter exceeded one model call");
    },
    onStepEnd: async ({ finishReason, usage, text }) => {
      try {
        await execution.checkpoint({
          node_id: `model:${modelTurn + 1}`, state: "completed", phase: "working", progress: Math.min(90, 15 + modelTurn * 6),
          // The control plane projects this model-owned text into the public SSE
          // stream for interactive invocations. Tool-only steps normally have no
          // text, while the final step supplies Markdown as it becomes durable.
          output: { finish_reason: finishReason, usage, text_delta: text },
        });
      } catch (error) {
        hookFailure ??= error;
      }
    },
    onToolExecutionStart: monitor.onStart,
    onToolExecutionEnd: monitor.onEnd,
  });

  let result: Awaited<ReturnType<typeof agent.stream>> | undefined;
  let streamAborted = false;
  const finish = async (completion: HarnessCompletion) => {
    await execution.complete({ ...completion, usage: result?.totalUsage as unknown as Record<string, unknown> | undefined });
    return { mistyRunId: input.mistyRunId, text: completion.text, steps: result?.steps.length ?? 0, incomplete: completion.status !== "success" };
  };
  const stopped = (): HarnessCompletion => {
    if (monitor.handoff) return { status: "success", text: monitor.handoff };
    const failures = monitor.failures();
    return {
      status: "incomplete", text: failures.length ? incompleteToolResultText(failures) : order.stoppedReason,
      error_code: "tool_sequence_stopped", error_message: order.stoppedReason,
    };
  };
  const limit = modelTurnLimit(context.model_turn_limit);
  const legacyDeadline = Date.now() + 30 * 60_000;
  try {
    const shared = {
      providerOptions: { gateway: { models: [context.model_id] } },
      onAbort: async () => { streamAborted = true; },
      runtimeContext: { mistyRunId: input.mistyRunId },
    };
    let messages: ModelMessage[] = initialMessages(context);
    const window = contextWindow(context.context_window_tokens);
    let lastInputTokens = 0;
    let lastSentBytes = 0;
    let compactions = 0;
    // Keeps the next call inside the model's window: clear old tool results
    // first, then summarize older steps (see src/context-compaction.ts).
    const manageContext = async () => {
      let estimate = lastInputTokens > 0 ? estimateInputTokens(lastInputTokens, lastSentBytes, messages) : messageBytes(messages) / 4;
      if (estimate > window * CLEAR_AT) {
        const before = messageBytes(messages);
        const cleared = clearOldToolResults(messages);
        if (cleared.cleared) {
          messages = cleared.messages;
          estimate *= messageBytes(messages) / before;
        }
      }
      if (estimate <= window * COMPACT_AT) return;
      const plan = planCompaction(messages);
      if (!plan) return;
      try {
        const summary = await summarizeOlderSteps(identity, context.model_id, plan.older, window, ++compactions);
        messages = applyCompaction(plan, summary);
      } catch (error) {
        // A failed summary leaves the transcript as it was; the step may still fit.
        console.error("[misty-agent-runtime] context compaction failed", error);
      }
    };
    for (; modelTurn < limit; modelTurn++) {
      const steering = await takeSteering(identity, `${modelTurn}:before`, false);
      for (const message of steering.messages) messages.push({ role: "user", content: message.text });
      await manageContext();
      const budget = await fetchExecutionBudget(identity, modelTurn + 1);
      lastSentBytes = messageBytes(messages);
      // Admission (billing's hold for this turn) happens before the model call and
      // outside the agent's hooks, so a refusal stops the call instead of being
      // swallowed while the model runs unmetered.
      await execution.checkpoint({
        node_id: `model:${modelTurn + 1}`, state: "running", phase: "thinking", progress: Math.min(85, 10 + modelTurn * 6),
        // Native serialized payload size only. Billing estimates tokens/rates.
        output: { input_bytes: new TextEncoder().encode(JSON.stringify({ system: instructions, messages, tools: explaining ? [] : catalog.tools })).byteLength },
      });
      const current = await agent.stream({ messages, ...shared, timeout: modelTimeout(budget, Date.now(), legacyDeadline) });
      if (hookFailure) throw hookFailure;
      lastInputTokens = current.steps.at(-1)?.usage?.inputTokens ?? 0;
      result = { ...current, steps: [...(result?.steps ?? []), ...current.steps], totalUsage: accumulateModelUsage(result?.totalUsage, current.totalUsage) };
      // WorkflowAgent returns the complete model transcript, including tool
      // results. Reinject our fixed system instructions once on the next call.
      messages = current.messages.filter((message) => message.role !== "system");
      if (streamAborted || order.stoppedReason) break;
      if (order.resume()) {
        // The model plans again from the failure or stale screen it just saw.
        if (monitor.requiredInspection) messages.push({ role: "user", content: browserReinspectionInstruction(monitor.requiredInspection) });
        continue;
      }
      if (finalTaskCompletion(current.steps, current.toolResults) || current.finishReason !== "tool-calls" || current.steps.length !== 1) {
        const finalSteering = await takeSteering(identity, `${modelTurn}:finish`, true);
        if (!finalSteering.messages.length) break;
        for (const message of finalSteering.messages) messages.push({ role: "user", content: message.text });
      }
    }
    if (!result) throw new Error("empty_agent_response");
  } catch (error) {
    if (order.stoppedReason) return await finish(stopped());
    const failure = classifyRuntimeError(error);
    await finish({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
    throw new FatalError(failure.message);
  }

  const taskReport = explaining ? undefined : finalTaskCompletion(result.steps, result.toolResults);
  const unfinished = unfinishedModelResult(streamAborted, result.steps.length, taskReport ? "stop" : result.finishReason, limit);
  if (unfinished && streamAborted) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  if (order.stoppedReason) return await finish(stopped());
  if (unfinished) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  const text = finalText(result.steps);
  // A run that took no actions and answered in text (a greeting, or a question
  // answered directly) is a reply, with nothing to confirm. Any run that acted
  // must still confirm its outcome with misty_finish_task.
  const acted = result.steps.some((step) => step.content.some((part) => part.type.startsWith("tool-")));
  if (!explaining && !taskReport && !acted && text) return await finish({ status: "success", text });
  if (!explaining) {
    const outcome = taskCompletionOutcome(taskReport);
    return await finish({ ...outcome, text: taskReport ? taskCompletionText(taskReport) : text || outcome.error_message! });
  }
  if (!text) {
    return await finish({
      status: "incomplete", text: "I couldn't fully complete that request because no final answer was produced.",
      error_code: "empty_agent_response", error_message: "The agent produced no final answer.",
    });
  }
  return await finish({ status: "success", text });
}
