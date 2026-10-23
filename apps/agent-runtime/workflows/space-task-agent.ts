import { admissionInput } from "../src/admission-input.js";
import { type ModelMessage } from "ai";
import { FatalError } from "workflow";
import { browserReinspectionInstruction } from "../src/browser-reinspection.js";
import { summarizeOlderSteps } from "../src/context/compaction-step.js";
import { closingAnswer } from "../src/closing-answer.js";
import { contextWindow, messageBytes } from "../src/context/context-compaction.js";
import { condense, oldToolResults, periodicSummary, staleSnapshots, SUMMARY_EVERY_TURNS, summarizedSteps, workingSet } from "../src/context/condenser.js";
import { admitModelTurn, fetchExecutionBudget, takeSteering } from "../src/control-plane-steps.js";
import type { HarnessCompletion } from "../src/harness.js";
import { discoverRemoteMCPTools } from "../src/mcp-runtime.js";
import { accumulateModelUsage, modelTurnLimit } from "../src/model-budget.js";
import { initialMessages } from "../src/model-input.js";
import { runBrowserReplay, validReplay } from "../src/browser-replay.js";
import { classifyRuntimeError } from "../src/runtime-errors.js";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import { screenJoined } from "../src/screen-session.js";
import { Ledger, ledgerFromPrompt } from "../src/ledger.js";
import { ledgerNote, requirementsLedger, worthLedger } from "../src/requirements-review.js";
import { contextParts } from "../src/context/working-set.js";
import { finalTaskCompletion, finishMissingNote, isProgressNote, progressNoteNudge } from "../src/task-completion.js";
import { toolState } from "../src/toolsets.js";
import { finalText, incompleteToolResultText } from "../src/tool-outcomes.js";
import type { AgentRun } from "./agent-run.js";
import { verifyFinish } from "./finish-review.js";
import { streamTurn } from "./model-stream.js";
import { pushOpenedPages } from "./page-state.js";
import { endRun } from "./run-ending.js";
import { startRun, type SpaceTaskWorkflowInput } from "./run-start.js";
import { assembleTools } from "./run-tools.js";
import { logContextBreakdown, logToolChange, paceTurn, waitOutTakeover, type Pacing } from "./turn-pacing.js";

/**
 * One durable agent run. The model sees every tool in the run's catalog under
 * its real name; Misty's gateway authorizes, journals and executes each call.
 * The model reports the outcome with agent_finish.
 */
const usageWrapUpInstruction = "Usage note from Misty: this account just reached an AI usage limit, so this task has only a little room left. Finish now in as few steps as possible. If the task cannot be completed, call agent_finish and report exactly what is done and what remains.";

export async function runSpaceTaskAgent(input: SpaceTaskWorkflowInput) {
  "use workflow";
  const started = await startRun(input);
  if (started.kind === "done") return started.result;
  const { identity, context, execution, rejected, documents } = started;
  const mcpCatalog = await discoverRemoteMCPTools(identity);

  const run = {
    identity, context, execution, rejected, documents,
    ledger: new Ledger(ledgerFromPrompt(context.prompt)),
    order: serialToolLifecycle(),
    limit: 0, modelTurn: 0, visibleTools: [], hookFailure: undefined,
    verified: false, persisted: 0,
  } as Omit<AgentRun, "tools"> as AgentRun;
  const { order } = run;
  let finishAsked = false;
  let progressNudges = 0;
  let wrappingUp = false;
  run.tools = assembleTools(run, mcpCatalog);
  run.visibleTools = run.tools.toolNames;
  await execution.checkpoint({
    node_id: "mcp:catalog", state: "completed", phase: "tools_ready", progress: 8,
    output: {
      advertised_tool_count: run.tools.advertised.size,
      missing_allowed_tools: context.allowed_tools.filter((name) => !run.tools.advertised.has(name)),
      omitted_tools: run.tools.catalog.omitted,
    },
  });

  let result: Awaited<ReturnType<typeof run.tools.agent.stream>> | undefined;
  let streamAborted = false;
  const finish = async (completion: HarnessCompletion) => {
    await execution.complete({ ...completion, usage: result?.totalUsage as unknown as Record<string, unknown> | undefined });
    return { mistyRunId: input.mistyRunId, text: completion.text, steps: result?.steps.length ?? 0, incomplete: completion.status !== "success" };
  };
  const stopped = (): HarnessCompletion => {
    const { monitor } = run.tools;
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
    if (!run.tools.monitor.handoff && (result?.steps.length ?? 0) > 2) {
      const answer = await closingAnswer(identity, context.model_id, context.prompt, transcript, order.stoppedReason);
      if (answer) return await finish({ ...ending, text: `${answer}\n\n${ending.text}` });
    }
    return await finish(ending);
  };
  const limit = run.limit = modelTurnLimit(context.model_turn_limit);
  const pacing: Pacing = { startedAt: Date.now(), pausedMs: 0, researchScale: 1 };
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
        available: (name) => run.tools.advertised.has(name),
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
    // Keeps the next call inside the model's window (src/context/condenser.ts).
    let summaryTurn = 0;
    const condensers = [staleSnapshots,
      periodicSummary((older) => summarizeOlderSteps(identity, context.model_id, older, window, ++compactions), () => run.modelTurn - summaryTurn >= SUMMARY_EVERY_TURNS, () => { summaryTurn = run.modelTurn; }),
      workingSet, oldToolResults,
      summarizedSteps((older) => summarizeOlderSteps(identity, context.model_id, older, window, ++compactions))];
    // A long request's requirements, listed once before work starts (src/requirements-review.ts).
    if (worthLedger(context.prompt)) {
      const listed = await requirementsLedger(identity, context.model_id, context.prompt);
      if (listed) messages.push({ role: "user", content: ledgerNote(listed) });
    }
    // agent.tool survives a summary that drops the calls that chose it.
    let toolsetState = toolState(messages, run.tools.toolsets.initial);
    let lastToolKey = "";
    for (; run.modelTurn < limit; run.modelTurn++) {
      const modelTurn = run.modelTurn;
      const steering = await takeSteering(identity, `${modelTurn}:before`, false);
      for (const message of steering.messages) messages.push({ role: "user", content: message.text });
      if (steering.paused) messages = await waitOutTakeover(run, messages, pacing);
      messages = await condense(messages, { inputTokens: lastInputTokens, sentBytes: lastSentBytes, window }, condensers);
      // Where a long run's context goes (docs/agents/harness.md, "Context"): the largest parts, every 5 turns.
      if (modelTurn % 5 === 4) console.log(`[misty-agent-runtime] context_parts ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, ...contextParts(messages) })}`);
      const observed = toolState(messages, run.tools.toolsets.initial);
      if (observed.seen || !toolsetState.seen) toolsetState = observed;
      run.visibleTools = paceTurn(run, messages, pacing, run.tools.toolsets.visible(run.tools.toolNames, toolsetState));
      lastToolKey = logToolChange(run, lastToolKey);
      const budget = await fetchExecutionBudget(identity, modelTurn + 1);
      lastSentBytes = messageBytes(messages);
      // Admission (billing's hold for this turn) happens before the model call and
      // outside the agent's hooks, so a refusal stops the call instead of being
      // swallowed while the model runs unmetered.
      if (modelTurn === 0) logContextBreakdown(run, messages);
      const admission = await admitModelTurn(identity, {
        node_id: `model:${modelTurn + 1}`, state: "running", phase: "thinking", progress: Math.min(85, 10 + modelTurn * 6),
        // Native payload size only, with images counted apart from text. Billing estimates tokens/rates.
        output: admissionInput({ system: run.tools.instructions, messages, tools: run.tools.catalog.tools }),
      });
      // Past a usage limit the run may finish on a small grace; ask the model,
      // once, to wrap up rather than start new work.
      if (admission.over_limit && !wrappingUp) {
        wrappingUp = true;
        messages.push({ role: "user", content: usageWrapUpInstruction });
      }
      const current = await streamTurn(run, messages, shared, budget, pacing, modelTurn);
      if (run.hookFailure) throw run.hookFailure;
      lastInputTokens = current.steps.at(-1)?.usage?.inputTokens ?? 0;
      result = { ...current, steps: [...(result?.steps ?? []), ...current.steps], totalUsage: accumulateModelUsage(result?.totalUsage, current.totalUsage) };
      // WorkflowAgent returns the complete model transcript, including tool
      // results. Reinject our fixed system instructions once on the next call.
      messages = current.messages.filter((message) => message.role !== "system");
      transcript = messages;
      if (screenJoined(current.toolResults)) {
        // The agent's own tab joined this run: offer its browser tools from the next turn.
        run.tools = assembleTools(run, await discoverRemoteMCPTools(identity));
        // A summarized transcript may no longer show the run's last switch: keep the one it made.
        const rebuilt = toolState(messages, run.tools.toolsets.initial);
        if (rebuilt.seen || !toolsetState.seen) toolsetState = rebuilt;
      }
      await pushOpenedPages(run, current.toolResults, messages);
      if (streamAborted || order.stoppedReason) break;
      if (order.resume()) {
        // The model plans again from the failure or stale screen it just saw.
        if (run.tools.monitor.requiredInspection) messages.push({ role: "user", content: browserReinspectionInstruction(run.tools.monitor.requiredInspection) });
        continue;
      }
      // Narration counts only in a row: a turn that made calls resets it (see the progress line below).
      if (current.finishReason === "tool-calls") progressNudges = 0;
      const plainAnswer = finalText(current.steps);
      const actedSoFar = (result?.steps ?? []).some((step) => step.content.some((part) => part.type.startsWith("tool-")));
      // A plain-text answer after work goes through the same one verification as agent_finish
      // (src/finish-verification.ts), so a run can't skip it by answering in text.
      if (plainAnswer && actedSoFar && !isProgressNote(plainAnswer) && current.finishReason !== "tool-calls" && !finalTaskCompletion(current.steps, current.toolResults)) {
        const note = await verifyFinish(run, { outcome: "completed", summary: plainAnswer.slice(0, 12_000), remaining: [] }, messages);
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
  return await endRun(run, result, streamAborted, finish, finishStopped);
}
