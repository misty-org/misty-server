import { WorkflowAgent } from "@ai-sdk/workflow";
import { admissionInput } from "../src/admission-input.js";
import { withRuntimeInstructions } from "../src/prompt-cache.js";
import { isStepCount, tool, type ModelMessage, type ToolSet } from "ai";
import { FatalError, getWorkflowMetadata } from "workflow";
import { browserReinspectionInstruction } from "../src/browser-reinspection.js";
import { summarizeOlderSteps } from "../src/compaction-step.js";
import { contextWindow, messageBytes } from "../src/context-compaction.js";
import { condense, oldToolResults, staleCodeResults, staleSnapshots, summarizedSteps } from "../src/condenser.js";
import type { RuntimeIdentity } from "../src/control-plane.js";
import { activateRuntime, admitModelTurn, checkpoint, complete, fetchContext, fetchExecutionBudget, takeSteering } from "../src/control-plane-steps.js";
import { MISTY_HARNESS_VERSION, type HarnessCompletion, type HarnessExecution } from "../src/harness.js";
import { InstanceModel } from "../src/instance-model.js";
import { discoverRemoteMCPTools } from "../src/mcp-runtime.js";
import { accumulateModelUsage, modelTimeout, modelTurnLimit } from "../src/model-budget.js";
import { initialMessages } from "../src/model-input.js";
import { withCacheBreakpoint } from "../src/prompt-cache.js";
import { runBrowserReplay, validReplay } from "../src/browser-replay.js";
import { catalogTools, modelCatalog } from "../src/model-tools.js";
import { classifyRuntimeError } from "../src/runtime-errors.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { screenJoined } from "../src/screen-session.js";
import { afterRefusal, unbackedCompletion } from "../src/finish-check.js";
import { earlyGiveUp } from "../src/persistence-check.js";
import { deadlineNote } from "../src/turn-deadline.js";
import { finalTaskCompletion, taskCompletionInstructions, taskCompletionOutcome, taskCompletionSchema, taskCompletionText, taskCompletionTool } from "../src/task-completion.js";
import { executeTool } from "../src/tool-calls.js";
import { rejectedWithoutEffect } from "../src/tool-execution.js";
import { toolMonitor } from "../src/tool-monitor.js";
import { agentTools, toolLoadTool, toolState, toolUseTool } from "../src/toolsets.js";
import { modelFamilyNote } from "../src/model-family.js";
import { citationInstructions, citationRefusal, unreadCitations } from "../src/citation-check.js";
import { closedResearchTools, researchNote, researchUse } from "../src/research-budget.js";
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
 * The model reports the outcome with agent_finish.
 */
const usageWrapUpInstruction = "Usage note from Misty: this account just reached an AI usage limit, so this task has only a little room left. Finish now in as few steps as possible. If the task cannot be completed, call agent_finish and report exactly what is done and what remains.";

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
  if (context.routine_execution !== undefined) throw new FatalError("Retired automation execution cannot be resumed.");

  const order = serialToolLifecycle();
  const guard = (callId: string) => {
    const declined = order.declined(callId);
    if (declined) throw new Error(declined);
  };
  let finishRefused = false;
  let citationRefused = false;
  let persistenceRefused = false;
  // Recomputed from the transcript before every model call.
  let visibleTools: string[] = [];
  let modelTurn = 0;
  // The agent swallows errors thrown from its step hooks, so a failed settlement
  // checkpoint is held here and rethrown once the call returns.
  let hookFailure: unknown;
  let wrappingUp = false;
  // The run's tools and agent, built from Misty's catalog. A screen that joins the
  // run mid-run (screen_open opening the agent's own tab) rebuilds them, so browser
  // tools reach the model in this run instead of a hand-off to a new one.
  const assemble = (descriptors: typeof mcpCatalog.tools) => {
    const catalog = modelCatalog(descriptors, [taskCompletionTool, toolUseTool, toolLoadTool]);
    const advertised = new Set(descriptors.map((descriptor) => descriptor.name));
    const monitor = toolMonitor({ catalog, order, checkpoint: execution.checkpoint, rejected });
    // agent.tool: answer by default; browse, desktop, code and loaded families on request.
    const toolsets = agentTools(catalog);
    const researchTools = { search: catalog.modelName("web.search"), fetch: catalog.modelName("web.fetch"), open: catalog.modelName("screen.open") };
    const tools: ToolSet = {
      ...catalogTools(catalog, async (callId, name, value) => {
        guard(callId);
        return await execution.executeCapability(callId, name, value);
      }),
      ...toolsets.tools,
      [taskCompletionTool]: tool({
        description: "Report the final task outcome and user-facing answer. Call alone after completing all possible work, or when a real blocker prevents further progress. This records a report; it grants no capabilities and performs no external action.",
        inputSchema: taskCompletionSchema,
        execute: async (report, options) => {
          guard(options.toolCallId);
          // Refused once when it claims work no tool did (src/finish-check.ts); the model sees why.
          const messages = options.messages ?? [];
          // Refused once when it cites a page the run never read (src/citation-check.ts).
          if (!citationRefused) {
            const unread = unreadCitations(report.summary, messages, researchTools);
            if (unread.length) {
              citationRefused = true;
              const canFetch = Boolean(researchTools.fetch) && researchUse(messages, researchTools).stage !== "closed";
              return { status: "failure", tool_error: { message: citationRefusal(unread, canFetch) } };
            }
          }
          // Refused once when it gives up early with room left (src/persistence-check.ts).
        if (!persistenceRefused) {
          const unfinished = earlyGiveUp(report, modelTurn, limit);
          if (unfinished) {
            persistenceRefused = true;
            return { status: "failure", tool_error: { message: unfinished } };
          }
        }
        if (finishRefused) return afterRefusal(report, messages, catalog.readOnly);
          const refusal = unbackedCompletion(report, messages, catalog.readOnly);
          if (refusal) {
            finishRefused = true;
            return { status: "failure", tool_error: { message: refusal } };
          }
          return report;
        },
      }),
    };
    const toolNames = Object.keys(tools);
    const instructions = withRuntimeInstructions(context.system, [executionInstructions, taskCompletionInstructions, citationInstructions, toolsets.instructions, modelFamilyNote(context.model_id)].filter(Boolean));
    const agent = new WorkflowAgent({
      id: "misty-space-task-agent",
      model: new InstanceModel(context.model_id, identity),
      instructions,
      tools,
      prepareStep: () => ({ activeTools: monitor.activeTools(visibleTools) }),
      // One model call per stream lets the coordinator refresh the active-time
      // deadline after durable tool waits. The aggregate loop below owns the cap.
      stopWhen: isStepCount(1),
      maxRetries: 2,
      // Match the Go admission ceiling (agents.MaxAgentTurnOutputTokens).
      maxOutputTokens: 8_192,
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
    return { catalog, advertised, monitor, toolsets, researchTools, toolNames, instructions, agent };
  };
  let { catalog, advertised, monitor, toolsets, researchTools, toolNames, instructions, agent } = assemble(mcpCatalog.tools);
  visibleTools = toolNames;
  await execution.checkpoint({
    node_id: "mcp:catalog", state: "completed", phase: "tools_ready", progress: 8,
    output: {
      advertised_tool_count: advertised.size,
      missing_allowed_tools: context.allowed_tools.filter((name) => !advertised.has(name)),
      omitted_tools: catalog.omitted,
    },
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
    if (validReplay(context.browser_replay)) {
      // A workflow that ran before replays its saved browser steps without a
      // model; the agent takes over at the first step the page no longer matches.
      const replay = await runBrowserReplay(context.browser_replay, {
        run: (callId, name, value) => execution.executeCapability(callId, name, value),
        checkpoint: execution.checkpoint,
        available: (name) => advertised.has(name),
      });
      if (replay.kind === "completed" || replay.kind === "handoff") return await finish({ status: "success", text: replay.text });
      if (replay.kind === "stopped") {
        return await finish({ status: "incomplete", text: replay.text, error_code: "tool_sequence_stopped", error_message: replay.text });
      }
      if (replay.note) messages.push({ role: "user", content: replay.note });
    }
    const window = contextWindow(context.context_window_tokens);
    let lastInputTokens = 0;
    let lastSentBytes = 0;
    let compactions = 0;
    // Keeps the next call inside the model's window: clear old tool results
    // first, then summarize older steps (see src/context-compaction.ts).
    // Keeps the next call inside the model's window (src/condenser.ts).
    const condensers = [staleSnapshots, staleCodeResults, oldToolResults,
      summarizedSteps((older) => summarizeOlderSteps(identity, context.model_id, older, window, ++compactions))];
    // agent.tool survives a summary that drops the calls that chose it.
    let toolsetState = toolState(messages, toolsets.initial);
    for (; modelTurn < limit; modelTurn++) {
      const steering = await takeSteering(identity, `${modelTurn}:before`, false);
      for (const message of steering.messages) messages.push({ role: "user", content: message.text });
      messages = await condense(messages, { inputTokens: lastInputTokens, sentBytes: lastSentBytes, window }, condensers);
      const observed = toolState(messages, toolsets.initial);
      if (observed.seen || !toolsetState.seen) toolsetState = observed;
      visibleTools = toolsets.visible(toolNames, toolsetState);
      // Research budget (src/research-budget.ts): a run that keeps searching is told what it
      // has spent, then loses search and fetch so its next turn can only answer.
      const research = researchUse(messages, researchTools);
      const closed = closedResearchTools(research, researchTools);
      if (closed.size) visibleTools = visibleTools.filter((name) => !closed.has(name));
      const researchWarning = researchNote(research, messages, researchTools);
      if (researchWarning) messages.push({ role: "user", content: researchWarning });
      // The deliverable deadline (src/turn-deadline.ts): near the turn limit the run writes its
      // answer from what it found instead of ending with nothing.
      const deadline = deadlineNote(modelTurn, limit, messages);
      if (deadline) messages.push({ role: "user", content: deadline });
      if (modelTurn >= limit - 1) visibleTools = visibleTools.filter((name) => name === taskCompletionTool);
      const budget = await fetchExecutionBudget(identity, modelTurn + 1);
      lastSentBytes = messageBytes(messages);
      // Admission (billing's hold for this turn) happens before the model call and
      // outside the agent's hooks, so a refusal stops the call instead of being
      // swallowed while the model runs unmetered.
      const admission = await admitModelTurn(identity, {
        node_id: `model:${modelTurn + 1}`, state: "running", phase: "thinking", progress: Math.min(85, 10 + modelTurn * 6),
        // Native payload size only, with images counted apart from text. Billing estimates tokens/rates.
        output: admissionInput({ system: instructions, messages, tools: catalog.tools }),
      });
      // Past a usage limit the run may finish on a small grace; ask the model,
      // once, to wrap up rather than start new work.
      if (admission.over_limit && !wrappingUp) {
        wrappingUp = true;
        messages.push({ role: "user", content: usageWrapUpInstruction });
      }
      const current = await agent.stream({ messages: withCacheBreakpoint(messages, context.model_id), ...shared, timeout: modelTimeout(budget, Date.now(), legacyDeadline) });
      if (hookFailure) throw hookFailure;
      lastInputTokens = current.steps.at(-1)?.usage?.inputTokens ?? 0;
      result = { ...current, steps: [...(result?.steps ?? []), ...current.steps], totalUsage: accumulateModelUsage(result?.totalUsage, current.totalUsage) };
      // WorkflowAgent returns the complete model transcript, including tool
      // results. Reinject our fixed system instructions once on the next call.
      messages = current.messages.filter((message) => message.role !== "system");
      if (screenJoined(current.toolResults)) {
        // The agent's own tab joined this run: offer its browser tools from the next turn.
        const next = await discoverRemoteMCPTools(identity);
        if (next.supported) {
          ({ catalog, advertised, monitor, toolsets, researchTools, toolNames, instructions, agent } = assemble(next.tools));
          toolsetState = toolState(messages, toolsets.initial);
        }
      }
      if (streamAborted || order.stoppedReason) break;
      if (order.resume()) {
        // The model plans again from the failure or stale screen it just saw.
        if (monitor.requiredInspection) messages.push({ role: "user", content: browserReinspectionInstruction(monitor.requiredInspection) });
        continue;
      }
      // A plain-text final answer is held to the same rule as agent_finish: refused once when
      // it cites a page the run only saw in search results (src/citation-check.ts).
      const plainAnswer = finalText(current.steps);
      if (!citationRefused && plainAnswer && current.finishReason !== "tool-calls" && !finalTaskCompletion(current.steps, current.toolResults)) {
        const unread = unreadCitations(plainAnswer, messages, researchTools);
        if (unread.length) {
          citationRefused = true;
          const canFetch = Boolean(researchTools.fetch) && researchUse(messages, researchTools).stage !== "closed";
          messages.push({ role: "user", content: citationRefusal(unread, canFetch, "give your answer again") });
          continue;
        }
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
    if (failure.code === "usage_limit_reached") {
      // Out of usage mid-task is a pause, not a failure: keep what was done.
      const sofar = result ? finalText(result.steps) : "";
      return await finish({ status: "incomplete", text: [sofar, failure.message].filter(Boolean).join("\n\n"), error_code: failure.code, error_message: failure.message });
    }
    await finish({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
    throw new FatalError(failure.message);
  }

  const taskReport = finalTaskCompletion(result.steps, result.toolResults);
  const unfinished = unfinishedModelResult(streamAborted, result.steps.length, taskReport ? "stop" : result.finishReason, limit);
  if (unfinished && streamAborted) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  if (order.stoppedReason) return await finish(stopped());
  if (unfinished) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  const text = finalText(result.steps);
  // A run that took no actions and answered in text (a greeting, or a question
  // answered directly) is a reply, with nothing to confirm. Any run that acted
  // must still confirm its outcome with agent_finish.
  const acted = result.steps.some((step) => step.content.some((part) => part.type.startsWith("tool-")));
  if (!taskReport && !acted && text) return await finish({ status: "success", text });
  const outcome = taskCompletionOutcome(taskReport);
  return await finish({ ...outcome, text: taskReport ? taskCompletionText(taskReport) : text || outcome.error_message! });
}
