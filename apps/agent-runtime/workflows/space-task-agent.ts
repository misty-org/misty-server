import { WorkflowAgent } from "@ai-sdk/workflow";
import { admissionInput } from "../src/admission-input.js";
import { withRuntimeInstructions } from "../src/prompt-cache.js";
import { isStepCount, tool, type ModelMessage, type ToolSet } from "ai";
import { FatalError, getWorkflowMetadata, sleep } from "workflow";
import { afterTakeover, TAKEOVER_LIMIT, TAKEOVER_POLL } from "../src/person-takeover.js";
import { browserReinspectionInstruction } from "../src/browser-reinspection.js";
import { summarizeOlderSteps } from "../src/compaction-step.js";
import { closingAnswer } from "../src/closing-answer.js";
import { DocumentIds } from "../src/document-ids.js";
import { contextWindow, messageBytes } from "../src/context-compaction.js";
import { condense, oldToolResults, periodicSummary, staleCodeResults, staleSnapshots, SUMMARY_EVERY_TURNS, summarizedSteps, workingSet } from "../src/condenser.js";
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
import { classifyRuntimeError, providerOutage } from "../src/runtime-errors.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { screenJoined } from "../src/screen-session.js";
import { afterRefusal, unbackedCompletion } from "../src/finish-check.js";
import { finishFindings, mayVerify, verificationNote } from "../src/finish-verification.js";
import { ledgerNote, openPages, requirementsLedger, reviewAnswer, reviewRefusal, worthLedger, worthReviewing } from "../src/requirements-review.js";
import { deadlineNote } from "../src/turn-deadline.js";
import { estimateMinutes, resultPrints, stillProgressing, timeBudget } from "../src/time-budget.js";
import { persistAgain } from "../src/persistence-check.js";
import { loopNote } from "../src/loop-guard.js";
import { openedScopes, PAGE_STATE_CHARS, pageStateMessages } from "../src/opened-pages.js";
import { contextParts } from "../src/working-set.js";
import { closingText, finalTaskCompletion, finishMissingNote, isProgressNote, progressNoteNudge, summaryMissingNote, taskCompletionInputSchema, taskCompletionInstructions, taskCompletionOutcome, taskCompletionText, taskCompletionTool, withAnswerSummary, type TaskCompletion } from "../src/task-completion.js";
import { runTeachTurn } from "../src/teach-turn.js";
import { executeTool } from "../src/tool-calls.js";
import { rejectedWithoutEffect } from "../src/tool-execution.js";
import { toolMonitor } from "../src/tool-monitor.js";
import { agentTools, toolLoadTool, toolState, toolUseTool } from "../src/toolsets.js";
import { modelFamilyNote } from "../src/model-family.js";
import { citationInstructions } from "../src/citation-check.js";
import { budgetScale, closedResearchTools, researchNote, researchUse } from "../src/research-budget.js";
import { executionInstructions, finalText, incompleteToolResultText, unfinishedModelResult } from "../src/tool-outcomes.js";
import type { SpaceTaskContext } from "../src/types.js";

/** How long a run waits out each provider outage before trying the same turn again. */
const OUTAGE_WAITS = ["30s", "60s", "120s", "240s"] as const;

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
  // Each tab's latest documentId, so a garbled copy in an action is repaired (src/document-ids.ts).
  const documents = new DocumentIds();
  const execution: HarnessExecution = {
    executeCapability: async (callId, name, value) => {
      try {
        const input = documents.repair(name, value);
        const output = await executeTool(identity, callId, name, input);
        documents.note(input, output);
        return output;
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
  if (context.teach) {
    // A lesson step: one streamed call, no tools (src/teach-turn.ts).
    try {
      const completion = await runTeachTurn(identity, context);
      await execution.complete(completion);
      return { mistyRunId: input.mistyRunId, text: completion.text, steps: 1, incomplete: completion.status !== "success" };
    } catch (error) {
      const failure = classifyRuntimeError(error);
      await execution.complete({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
      throw new FatalError(failure.message);
    }
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
  // A finish is verified once (src/finish-verification.ts); the next one stands.
  let verified = false;
  // Give-ups sent back after that turn, and what the run had found at the last one.
  let persisted = 0;
  // The largest research budget the run has had (src/research-budget.ts), kept across summaries.
  let researchScale = 1;
  let persistedAt: Set<string> | undefined;
  let finishAsked = false;
  let progressNudges = 0;
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
    const toolsets = agentTools(catalog, context.prompt);
    const researchTools = { search: catalog.modelName("web.search"), fetch: catalog.modelName("web.fetch"), open: catalog.modelName("screen.open") };
    const tools: ToolSet = {
      ...catalogTools(catalog, async (callId, name, value) => {
        guard(callId);
        return await execution.executeCapability(callId, name, value);
      }),
      ...toolsets.tools,
      [taskCompletionTool]: tool({
        description: "Report the final task outcome and user-facing answer. Call alone after completing all possible work, or when a real blocker prevents further progress. Leave summary out when your previous response already gave the answer as text. This records a report; it grants no capabilities and performs no external action.",
        inputSchema: taskCompletionInputSchema,
        execute: async (sent, options) => {
          guard(options.toolCallId);
          const messages = options.messages ?? [];
          // A finish after a text answer leaves summary out: that answer is the summary, so the
          // person reads it once (src/task-completion.ts).
          const report = withAnswerSummary(sent, messages);
          if (!report) return { status: "failure", tool_error: { message: summaryMissingNote } };
          // Every check runs together, once (src/finish-verification.ts); the next finish stands.
          const note = await verify(report, messages);
          if (note) return { status: "failure", tool_error: { message: note } };
          // A completed report that still claims work no tool did becomes partial when the
          // request named an app (src/finish-check.ts).
          return unbackedCompletion(report, messages, catalog.readOnly) ? afterRefusal(report, messages, catalog.readOnly) : report;
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
      // Backoff of 2, 4, 8 and 16 seconds rides out a short network blip to the provider,
      // which failed whole runs at their first call when three quick retries did not.
      maxRetries: 4,
      // Match the Go admission ceiling (agents.MaxAgentTurnOutputTokens).
      maxOutputTokens: 32_000,
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
  // The one verification turn (src/finish-verification.ts): the note that sends a finish or a
  // plain answer back, or undefined when it stands. Only the first finish with turns left is checked.
  const verify = async (report: TaskCompletion, messages: ModelMessage[]): Promise<string | undefined> => {
    const long = worthLedger(context.prompt);
    if (!mayVerify(verified, modelTurn, limit)) {
      // A long request that quits again with turns left goes back while it is still finding
      // things (src/persistence-check.ts); one that only repeats itself finishes.
      if (!long || !persistedAt) return undefined;
      const fresh = [...resultPrints(messages)].filter((print) => !persistedAt!.has(print)).length;
      const again = persistAgain(report, modelTurn, limit, persisted, fresh);
      console.log(`[misty-agent-runtime] finish_persist ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, refusals: persisted, fresh, sentBack: Boolean(again) })}`);
      if (!again) return undefined;
      persisted++;
      persistedAt = resultPrints(messages);
      return verificationNote([again], true);
    }
    verified = true;
    const { issues, gaveUp } = finishFindings({
      request: context.prompt, report, messages, turn: modelTurn, limit, long,
      research: researchTools, tabList: catalog.modelName("browser.tab.list"), readOnly: catalog.readOnly,
    });
    if (gaveUp) persistedAt = resultPrints(messages);
    if (long && worthReviewing(context.prompt, report, modelTurn, limit)) {
      const pages = openPages(messages, { open: researchTools.open, navigate: catalog.modelName("browser.navigate") });
      const gaps = await reviewAnswer(identity, context.model_id, context.prompt, report, pages);
      if (gaps.length) issues.push(reviewRefusal(gaps));
    }
    const note = verificationNote(issues, gaveUp);
    console.log(`[misty-agent-runtime] finish_verified ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, long, issues: issues.length })}`);
    return note;
  };
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
  // A stopped run writes its answer from what it found before the stop reason
  // (src/closing-answer.ts); a handoff or a run that barely started keeps the plain message.
  const finishStopped = async () => {
    const ending = stopped();
    if (!monitor.handoff && (result?.steps.length ?? 0) > 2) {
      const answer = await closingAnswer(identity, context.model_id, context.prompt, transcript, order.stoppedReason);
      if (answer) return await finish({ ...ending, text: `${answer}\n\n${ending.text}` });
    }
    return await finish(ending);
  };
  const limit = modelTurnLimit(context.model_turn_limit);
  const startedAt = Date.now();
  let pausedMs = 0;
  // The agent's own estimate of the task's minutes (its first checklist), kept past compaction.
  let estimate: number | undefined;
  const legacyDeadline = startedAt + 30 * 60_000;
  // The transcript so far, also read after the loop when a stopped run writes its answer.
  let transcript: ModelMessage[] = [];
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
    let summaryTurn = 0;
    const condensers = [staleSnapshots, staleCodeResults,
      periodicSummary((older) => summarizeOlderSteps(identity, context.model_id, older, window, ++compactions), () => modelTurn - summaryTurn >= SUMMARY_EVERY_TURNS, () => { summaryTurn = modelTurn; }),
      workingSet, oldToolResults,
      summarizedSteps((older) => summarizeOlderSteps(identity, context.model_id, older, window, ++compactions))];
    // agent.tool survives a summary that drops the calls that chose it.
    // A long request's requirements, listed once before work starts (src/requirements-review.ts).
    if (worthLedger(context.prompt)) {
      const ledger = await requirementsLedger(identity, context.model_id, context.prompt);
      if (ledger) messages.push({ role: "user", content: ledgerNote(ledger) });
    }
    let toolsetState = toolState(messages, toolsets.initial);
    let lastToolKey = "";
    for (; modelTurn < limit; modelTurn++) {
      const steering = await takeSteering(identity, `${modelTurn}:before`, false);
      for (const message of steering.messages) messages.push({ role: "user", content: message.text });
      // Taken over (src/person-takeover.ts): wait without model turns until the person resumes.
      if (steering.paused) {
        const sent: string[] = [];
        let paused = true;
        const pauseStarted = Date.now();
        for (let poll = 1; paused && poll <= TAKEOVER_LIMIT; poll++) {
          await sleep(TAKEOVER_POLL);
          const next = await takeSteering(identity, `${modelTurn}:paused:${poll}`, false);
          for (const message of next.messages) sent.push(message.text);
          paused = Boolean(next.paused);
        }
        // The person's turn in the window doesn't spend the agent's time.
        pausedMs += Date.now() - pauseStarted;
        messages = afterTakeover(messages, sent);
      }
      messages = await condense(messages, { inputTokens: lastInputTokens, sentBytes: lastSentBytes, window }, condensers);
      // Where a long run's context goes (docs/plans/agent-harness-v2.md): the largest parts, every 5 turns.
      if (modelTurn % 5 === 4) console.log(`[misty-agent-runtime] context_parts ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, ...contextParts(messages) })}`);
      const observed = toolState(messages, toolsets.initial);
      if (observed.seen || !toolsetState.seen) toolsetState = observed;
      visibleTools = toolsets.visible(toolNames, toolsetState);
      // Research budget (src/research-budget.ts): a run that keeps searching is told what it
      // has spent, then loses search and fetch so its next turn can only answer.
      researchScale = Math.max(researchScale, budgetScale(messages));
      const research = researchUse(messages, researchTools, researchScale);
      const closed = closedResearchTools(research, researchTools);
      if (closed.size) visibleTools = visibleTools.filter((name) => !closed.has(name));
      const researchWarning = researchNote(research, messages, researchTools);
      if (researchWarning) messages.push({ role: "user", content: researchWarning });
      // The loop guard (src/loop-guard.ts): the same call or address three times brings a note.
      const loop = loopNote(messages);
      if (loop) messages.push({ role: "user", content: loop });
      // The deliverable deadline (src/turn-deadline.ts): near the turn limit the run writes its
      // answer from what it found instead of ending with nothing.
      const deadline = deadlineNote(modelTurn, limit, messages);
      if (deadline) messages.push({ role: "user", content: deadline });
      // The person's time (src/time-budget.ts), against the agent's own estimate: paced at
      // 80%, wrapping up at 100%, only the answer past about 130%, unless the run is still
      // finding new things.
      estimate = estimateMinutes(messages) ?? estimate;
      const time = timeBudget(Date.now() - startedAt - pausedMs, messages, estimate, stillProgressing(messages));
      if (time.note) messages.push({ role: "user", content: time.note });
      if (modelTurn >= limit - 1 || time.answerOnly)
        visibleTools = visibleTools.filter((name) => name === taskCompletionTool);
      // Tool definitions come first in the request, so a change costs this turn its cached prefix.
      const toolKey = visibleTools.join(",");
      if (modelTurn > 0 && toolKey !== lastToolKey) {
        const before = new Set(lastToolKey.split(","));
        const now = new Set(visibleTools);
        console.log(`[misty-agent-runtime] tools_changed ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, added: visibleTools.filter((name) => !before.has(name)), removed: [...before].filter((name) => !now.has(name)) })}`);
      }
      lastToolKey = toolKey;
      const budget = await fetchExecutionBudget(identity, modelTurn + 1);
      lastSentBytes = messageBytes(messages);
      // Admission (billing's hold for this turn) happens before the model call and
      // outside the agent's hooks, so a refusal stops the call instead of being
      // swallowed while the model runs unmetered.
      // What the first turn carries (docs/plans/agent-harness-v2.md): the fixed cost of every turn.
      if (modelTurn === 0) {
        // The tools this turn sends, not the whole catalog: the rest wait behind agent_tool_use and loads.
        const sent = new Set(visibleTools);
        const toolSizes = catalog.tools.filter((tool) => sent.has(tool.modelName)).map((tool) => [tool.modelName, JSON.stringify(tool).length] as const);
        console.log(`[misty-agent-runtime] context_breakdown ${JSON.stringify({
          run: identity.mistyRunId,
          systemChars: instructions.length,
          toolChars: toolSizes.reduce((sum, [, size]) => sum + size, 0),
          tools: visibleTools.length,
          catalogTools: catalog.tools.length,
          toolSizes: [...toolSizes].sort((a, b) => b[1] - a[1]),
          messageChars: messageBytes(messages),
        })}`);
      }
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
      // A provider outage (src/runtime-errors.ts) is waited out, 30 s doubling to 4 min, and the
      // same turn tried again; the wait doesn't spend the run's time. Anything else still fails.
      const streamTurn = async () => {
        for (let outage = 0; ; outage++) {
          try {
            return await agent.stream({ messages: withCacheBreakpoint(messages, context.model_id), ...shared, timeout: modelTimeout(budget, Date.now(), legacyDeadline) });
          } catch (error) {
            if (outage >= OUTAGE_WAITS.length || hookFailure || !providerOutage(error)) throw error;
            console.log(`[misty-agent-runtime] provider_outage ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, wait: OUTAGE_WAITS[outage] })}`);
            const waitStarted = Date.now();
            await sleep(OUTAGE_WAITS[outage]!);
            pausedMs += Date.now() - waitStarted;
          }
        }
      };
      const current = await streamTurn();
      if (hookFailure) throw hookFailure;
      lastInputTokens = current.steps.at(-1)?.usage?.inputTokens ?? 0;
      result = { ...current, steps: [...(result?.steps ?? []), ...current.steps], totalUsage: accumulateModelUsage(result?.totalUsage, current.totalUsage) };
      // WorkflowAgent returns the complete model transcript, including tool
      // results. Reinject our fixed system instructions once on the next call.
      messages = current.messages.filter((message) => message.role !== "system");
      transcript = messages;
      if (screenJoined(current.toolResults)) {
        // The agent's own tab joined this run: offer its browser tools from the next turn.
        const next = await discoverRemoteMCPTools(identity);
        if (next.supported) {
          ({ catalog, advertised, monitor, toolsets, researchTools, toolNames, instructions, agent } = assemble(next.tools));
          // A summarized transcript may no longer show the run's last switch: keep the one it made.
          const rebuilt = toolState(messages, toolsets.initial);
          if (rebuilt.seen || !toolsetState.seen) toolsetState = rebuilt;
        }
      }
      // Pushed page state (src/opened-pages.ts): the pages this turn opened, shown before the
      // next turn, so the agent acts on them instead of spending a turn asking for each. A run
      // with browser_run reads pages in its programs instead (docs/plans/agent-harness-v3.md).
      const openTool = catalog.modelName("screen.open");
      const snapshotTool = catalog.modelName("browser.snapshot");
      if (openTool && snapshotTool && !catalog.modelName("browser.run")) {
        const scopes = openedScopes(current.toolResults as ReadonlyArray<{ toolName?: string; output?: unknown }>, openTool);
        for (const [index, scopeId] of scopes.entries()) {
          const callId = `page-state-${modelTurn}-${index}`;
          try {
            const state = await executeTool(identity, callId, "browser.snapshot", { scopeId, maxChars: PAGE_STATE_CHARS });
            documents.note({ scopeId }, state);
            messages.push(...pageStateMessages(callId, snapshotTool, scopeId, state));
          } catch {
            // The agent can still take the snapshot itself.
          }
        }
      }
      if (streamAborted || order.stoppedReason) break;
      if (order.resume()) {
        // The model plans again from the failure or stale screen it just saw.
        if (monitor.requiredInspection) messages.push({ role: "user", content: browserReinspectionInstruction(monitor.requiredInspection) });
        continue;
      }
      // Narration counts only in a row: a turn that made calls resets it (see the progress line below).
      if (current.finishReason === "tool-calls") progressNudges = 0;
      const plainAnswer = finalText(current.steps);
      const actedSoFar = (result?.steps ?? []).some((step) => step.content.some((part) => part.type.startsWith("tool-")));
      // A plain-text answer after work goes through the same one verification as agent_finish
      // (src/finish-verification.ts), so a run can't skip it by answering in text.
      if (plainAnswer && actedSoFar && !isProgressNote(plainAnswer) && current.finishReason !== "tool-calls" && !finalTaskCompletion(current.steps, current.toolResults)) {
        const note = await verify({ outcome: "completed", summary: plainAnswer.slice(0, 12_000), remaining: [] }, messages);
        if (note) {
          messages.push({ role: "user", content: note });
          continue;
        }
      }
      // A run that used its tools and then answered in plain text never said whether the work is
      // done, so it ended as unreported and the person saw a failure (31 Odysseys runs, one of
      // them a perfect answer). It is asked once to confirm with agent_finish.
      // The turn's progress line ("Last: … Next: …", or "Let me try the link directly.") written with
      // no tool call is not an answer: a run ended on one and the person got "Next: I'll read …
      // then finish" (October 19). Some models narrate most turns and now and then forget the
      // call (58 times in one Odysseys run), so it always goes back, unless the run has done
      // nothing but narrate for three turns in a row.
      if (plainAnswer && actedSoFar && isProgressNote(plainAnswer) && progressNudges < 3 && modelTurn < limit - 1) {
        progressNudges++;
        messages.push({ role: "user", content: progressNoteNudge });
        continue;
      }
      if (plainAnswer && actedSoFar && !finishAsked && current.finishReason !== "tool-calls" && !finalTaskCompletion(current.steps, current.toolResults) && modelTurn < limit - 1) {
        finishAsked = true;
        messages.push({ role: "user", content: finishMissingNote });
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
    if (order.stoppedReason) return await finishStopped();
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
  if (order.stoppedReason) return await finishStopped();
  if (unfinished) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  const text = closingText(result.steps);
  // A run that took no actions and answered in text (a greeting, or a question
  // answered directly) is a reply, with nothing to confirm. Any run that acted
  // must still confirm its outcome with agent_finish.
  const acted = result.steps.some((step) => step.content.some((part) => part.type.startsWith("tool-")));
  // How runs end (docs/plans/agent-harness-v2.md): most Odysseys runs ended as partial or unreported.
  console.log(`[misty-agent-runtime] run_finish ${JSON.stringify({ run: identity.mistyRunId, turns: modelTurn, reported: Boolean(taskReport), outcome: taskReport?.outcome, remaining: taskReport?.remaining.map((item) => item.requirement.slice(0, 120)), acted, plainText: !taskReport && Boolean(text) })}`);
  if (!taskReport && !acted && text) return await finish({ status: "success", text });
  const outcome = taskCompletionOutcome(taskReport);
  return await finish({ ...outcome, text: taskReport ? taskCompletionText(taskReport) : text || outcome.error_message! });
}
