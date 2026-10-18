import type { TaskCompletion } from "./task-completion.js";

// Long requests need persistence (docs/plans/odysseys-campaign.md): the first Odysseys
// batch finished "partial" after a minute or two with most of each run's turns unused,
// usually right after one page or tool failed. A partial or blocked finish is refused
// once while the run has room, unless every unfinished part needs the person.

/** Unfinished parts only the person can unblock: an account, a payment, a human check. */
const needsPerson = /\b(sign[ -]?in|log[ -]?in|password|account|captcha|verification|verify|2fa|one-time code|payment|pay|purchase|checkout|paywall|subscri)/i;

/** The refusal message, or undefined when the finish should stand. */
export function earlyGiveUp(report: TaskCompletion, turn: number, limit: number): string | undefined {
  if (report.outcome === "completed" || !report.remaining.length) return undefined;
  if (turn >= limit / 2) return undefined;
  const open = report.remaining.filter((item) => !needsPerson.test(`${item.requirement} ${item.reason}`));
  if (!open.length) return undefined;
  const list = open.map((item) => `- ${item.requirement}`).join("\n");
  return `Not finished yet, and this run still has most of its time. These parts remain:\n${list}\n` +
    "Work each one another way before reporting: find the right page with web_search (add site: and the domain), open it in your own tab (screen_open, then browser_navigate), read it with browser_snapshot, and use the site's own search, filters and links. A page or tool that failed is a reason to try another route, not to stop. " +
    "Report partial only for parts that need the person (a sign-in, a payment, a CAPTCHA) or that the sites truly do not have, and say what you tried.";
}
