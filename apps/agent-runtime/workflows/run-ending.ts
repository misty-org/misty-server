import type { HarnessCompletion } from "../src/harness.js";
import { ledgerSuffix } from "../src/ledger.js";
import { closingText, finalTaskCompletion, taskCompletionOutcome, taskCompletionText } from "../src/task-completion.js";
import { keepsAnswerAfterOddStop, unfinishedModelResult } from "../src/tool-outcomes.js";
import type { AgentRun } from "./agent-run.js";
import type { RunTools } from "./run-tools.js";

type StreamResult = Awaited<ReturnType<RunTools["agent"]["stream"]>>;

/** How a run that left its loop ends: its report, its answer, or why it stopped. */
export async function endRun<T>(
  run: AgentRun,
  result: StreamResult,
  streamAborted: boolean,
  finish: (completion: HarnessCompletion) => Promise<T>,
  finishStopped: () => Promise<T>,
): Promise<T> {
  const { order, ledger } = run;
  const taskReport = finalTaskCompletion(result.steps, result.toolResults);
  const unfinished = unfinishedModelResult(streamAborted, result.steps.length, taskReport ? "stop" : result.finishReason, run.limit);
  if (unfinished && streamAborted) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  if (order.stoppedReason) return await finishStopped();
  const answerText = closingText(result.steps);
  if (unfinished && keepsAnswerAfterOddStop(unfinished, result.finishReason, answerText, run.tools.monitor.failures().length)) {
    // The answer is kept, with the reason after it (the app shows the answer, then the reason).
    return await finish({
      status: "incomplete",
      text: `${answerText.trim()}\n\nI couldn't fully complete that request. The model stopped without confirming it was done.`,
      error_code: unfinished.code, error_message: unfinished.message,
    });
  }
  if (unfinished) return await finish({ status: "incomplete", text: unfinished.message, error_code: unfinished.code, error_message: unfinished.message });
  const text = answerText;
  // A run that took no actions and answered in text (a greeting, or a question
  // answered directly) is a reply, with nothing to confirm. Any run that acted
  // must still confirm its outcome with agent_finish.
  const acted = result.steps.some((step) => step.content.some((part) => part.type.startsWith("tool-")));
  // How runs end (docs/agents/harness.md, Completion): most Odysseys runs ended as partial or unreported.
  console.log(`[misty-agent-runtime] run_finish ${JSON.stringify({ run: run.identity.mistyRunId, turns: run.modelTurn, reported: Boolean(taskReport), outcome: taskReport?.outcome, remaining: taskReport?.remaining.map((item) => item.requirement.slice(0, 120)), acted, plainText: !taskReport && Boolean(text) })}`);
  if (!taskReport && !acted && text) return await finish({ status: "success", text });
  const outcome = taskCompletionOutcome(taskReport);
  return await finish({ ...outcome, text: taskReport ? taskCompletionText(taskReport) + ledgerSuffix(ledger) : text || outcome.error_message! });
}
