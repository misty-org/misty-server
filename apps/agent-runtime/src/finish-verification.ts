import type { ModelMessage } from "ai";
import { citationRefusal, unreadCitations } from "./citation-check.js";
import { unbackedCompletion } from "./finish-check.js";
import { earlyGiveUp } from "./persistence-check.js";
import { brokenProofTabs, proofTabsRefusal } from "./proof-tabs.js";
import { countRefusal } from "./count-check.js";
import { researchUse, type ResearchTools } from "./research-budget.js";
import { ledgerIssue, type LedgerRow } from "./ledger.js";
import type { TaskCompletion } from "./task-completion.js";

// One verification turn before a finish stands (docs/agents/harness.md, Completion). Until October 20
// each guard sent a finish back on its own: unread citations, unbacked claims, proof tabs, the
// requirements review, a plain answer that skipped the last refusal, and early give-ups up to
// eight times. Stacked, they kept short questions working after their answer was written: an
// answer that said one release date was unconfirmed went back to fetch the same site again and
// again. Now every check runs together once, the run gets one note with all it found, and its
// next finish stands. Sending a partial finish back to work is for long requests only.

export interface FinishCheck {
  request: string;
  report: TaskCompletion;
  messages: ModelMessage[];
  turn: number;
  limit: number;
  /** A long request (src/requirements-review.ts, worthLedger): many parts, worth persisting on. */
  long: boolean;
  research: ResearchTools;
  /** browser.tab.list's model name, when the run has tabs of its own. */
  tabList?: string;
  /** browser_run's model name, when the run has programs: the run checks its tabs with one. */
  program?: string;
  readOnly: (name: string) => boolean;
  /** Rows the run marked done that its calls do not back (src/ledger.ts). */
  unbackedRows?: LedgerRow[];
}

/** Whether a finish may still be sent back: once, and only with turns left to act on it. */
export function mayVerify(verified: boolean, turn: number, limit: number): boolean {
  return !verified && turn < limit - 2;
}

/** What the checks that need no model call find, in the order the run should fix them. */
export function finishIssues(check: FinishCheck): string[] {
  return finishFindings(check).issues;
}

/** The issues, and whether one is a long request giving up with time left. */
export function finishFindings(check: FinishCheck): { issues: string[]; gaveUp: boolean } {
  const issues: string[] = [];
  const unread = unreadCitations(check.report.summary, check.messages, check.research);
  if (unread.length) {
    const canFetch = Boolean(check.research.fetch) && researchUse(check.messages, check.research).stage !== "closed";
    issues.push(citationRefusal(unread, canFetch, "finish"));
  }
  const unbacked = unbackedCompletion(check.report, check.messages, check.readOnly);
  if (unbacked) issues.push(unbacked);
  const proof = check.tabList || check.program ? proofTabsRefusal(check.request, check.messages, check.tabList, check.program) : undefined;
  if (proof) issues.push(proof);
  // Checked on evidence, without a model call (docs/agents/harness.md, Completion).
  const broken = brokenProofTabs(check.messages);
  if (broken.length)
    issues.push(`Pages you kept as proof show an error or nothing: ${broken.join("; ")}. Open each page again from a search or the site's own links, keep() the working page, and close or replace the broken one.`);
  const unbackedLedger = ledgerIssue(check.unbackedRows ?? []);
  if (unbackedLedger) issues.push(unbackedLedger);
  const counts = countRefusal(check.request, check.report.summary);
  if (counts) issues.push(counts);
  const giveUp = check.long ? earlyGiveUp(check.report, check.turn, check.limit) : undefined;
  if (giveUp) issues.push(giveUp);
  return { issues, gaveUp: Boolean(giveUp) };
}

/**
 * The one note that sends a finish back, or undefined when it stands. `persist` is a long request
 * that gave up with time left: it is told to keep working, since a later give-up goes back again
 * while it is still finding things (src/persistence-check.ts).
 */
export function verificationNote(issues: string[], persist = false): string | undefined {
  if (!issues.length) return undefined;
  const body = issues.length === 1 ? issues[0]! : issues.map((issue, index) => `${index + 1}. ${issue}`).join("\n\n");
  const close = persist
    ? "Keep working on the open parts, then finish with agent_finish when they are done or truly blocked. Report partial only for what needs the person or what the sites do not have."
    : "Fix what you can, then finish with agent_finish. This check runs only once: your next finish stands as you send it, " +
      "so say plainly in it what is still unconfirmed rather than looking for it again.";
  return `Before your answer goes out, Misty checked it ${persist ? "" : "once "}against the request and this run:\n\n${body}\n\n${close}`;
}
