import type { ModelMessage } from "ai";
import { finishFindings, mayVerify, reportOnlyIssue, verificationNote } from "../src/finish-verification.js";
import { persistAgain } from "../src/persistence-check.js";
import { openPages, reviewAnswer, reviewRefusal, worthLedger, worthReviewing } from "../src/requirements-review.js";
import type { TaskCompletion } from "../src/task-completion.js";
import { resultPrints } from "../src/time-budget.js";
import type { AgentRun } from "./agent-run.js";

/**
 * The one verification turn (src/finish-verification.ts): the note that sends a finish or a
 * plain answer back, or undefined when it stands. Only the first finish with turns left is checked.
 */
export async function verifyFinish(run: AgentRun, report: TaskCompletion, messages: ModelMessage[]): Promise<string | undefined> {
  const { context, identity, limit, modelTurn } = run;
  const { catalog, researchTools } = run.tools;
  // Done rows nothing in the run backs go back to pending, whether or not this finish is checked.
  const unbacked = run.ledger.reconcile(messages, report.summary);
  // Checked on every finish, twice at most: a report of the work is never the answer.
  const reportedOnly = run.reportedOnly < 2 && modelTurn < limit - 2 ? reportOnlyIssue(report.summary) : undefined;
  if (reportedOnly) {
    run.reportedOnly++;
    return verificationNote([reportedOnly]);
  }
  const long = worthLedger(context.prompt);
  if (!mayVerify(run.verified, modelTurn, limit)) {
    // A long request that quits again with turns left goes back while it is still finding
    // things (src/persistence-check.ts); one that only repeats itself finishes.
    if (!long || !run.persistedAt) return undefined;
    const fresh = [...resultPrints(messages)].filter((print) => !run.persistedAt!.has(print)).length;
    const again = persistAgain(report, modelTurn, limit, run.persisted, fresh);
    console.log(`[misty-agent-runtime] finish_persist ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, refusals: run.persisted, fresh, sentBack: Boolean(again) })}`);
    if (!again) return undefined;
    run.persisted++;
    run.persistedAt = resultPrints(messages);
    return verificationNote([again], true);
  }
  run.verified = true;
  const { issues, gaveUp } = finishFindings({
    unbackedRows: unbacked, openRows: run.ledger.remaining(),
    request: context.prompt, report, messages, turn: modelTurn, limit, long,
    research: researchTools, tabList: catalog.modelName("browser.tab.list"), program: catalog.modelName("browser.run"), readOnly: catalog.readOnly,
  });
  if (gaveUp) run.persistedAt = resultPrints(messages);
  if (long && worthReviewing(context.prompt, report, modelTurn, limit)) {
    const pages = openPages(messages, { open: researchTools.open, navigate: catalog.modelName("browser.navigate") });
    const gaps = await reviewAnswer(identity, context.model_id, context.prompt, report, pages);
    if (gaps.length) issues.push(reviewRefusal(gaps));
  }
  const note = verificationNote(issues, gaveUp);
  console.log(`[misty-agent-runtime] finish_verified ${JSON.stringify({ run: identity.mistyRunId, turn: modelTurn, long, issues: issues.length })}`);
  return note;
}
