import type { ModelMessage } from "ai";
import { z } from "zod";
import type { TaskCompletion } from "./task-completion.js";

// The requirements ledger (docs/agents/checkpoints.md). A run with several parts keeps one row per
// part. A part is done only with evidence that outlives the model's say-so: a page it kept, a note or
// file it wrote, or a receipt a tool returned. A resume carries the ledger, so the parts that passed
// are not done again.

export const ledgerTool = "agent_ledger";

const evidenceSchema = z.object({
  kind: z.enum(["tab", "note", "file", "receipt"]),
  /** The address, note title, file path or receipt id that shows up in this run's tool calls. */
  ref: z.string().trim().min(1).max(500),
  summary: z.string().trim().max(500).default(""),
});
const rowSchema = z.object({
  /** Stable across updates; a new requirement may leave it out. */
  id: z.string().trim().min(1).max(40).optional(),
  requirement: z.string().trim().min(1).max(500),
  state: z.enum(["pending", "done", "failed", "blocked", "skipped"]),
  evidence: evidenceSchema.optional(),
  reason: z.string().trim().max(500).optional(),
  /** What a person has to do before this can go on: give a detail, or take over the browser. */
  needs: z.enum(["none", "input", "takeover"]).optional(),
  /** What the person said about it on a resume (a link to use, a birthday). */
  hint: z.string().trim().max(500).optional(),
});
export const ledgerInputSchema = z.object({ rows: z.array(rowSchema).min(1).max(40) });

export type LedgerRow = Omit<z.infer<typeof rowSchema>, "id"> & { id: string; carried?: boolean };
type LedgerEvidence = z.infer<typeof evidenceSchema>;

export const ledgerInstructions =
  `When a request has several parts, call ${ledgerTool} early with one row per part (state pending), and again as each part ends: ` +
  "done with evidence (a kept page's address, a note or file you wrote, or a receipt a tool returned), or failed, blocked or skipped with a reason. " +
  "Say needs: input when only the person can supply something, takeover for a captcha or sign-in. " +
  "Rows from an earlier attempt that are already done stay done: don't redo them. Work only the open rows.";

const marker = "<!--misty-ledger:";

/** The rows a run keeps, in order, updated by id or requirement. */
export class Ledger {
  private rows: LedgerRow[] = [];

  constructor(seed: LedgerRow[] = []) {
    this.rows = seed.map((row) => ({ ...row, carried: row.state === "done" }));
  }

  get size() {
    return this.rows.length;
  }

  snapshot(): LedgerRow[] {
    return this.rows.map((row) => ({ ...row }));
  }

  update(input: z.infer<typeof ledgerInputSchema>): LedgerRow[] {
    for (const sent of input.rows) {
      const match = this.rows.find((row) => (sent.id ? row.id === sent.id : false) || row.requirement === sent.requirement);
      if (!match) {
        this.rows.push({ ...sent, id: sent.id ?? `r${this.rows.length + 1}` });
        continue;
      }
      // A carried row the run reopens is no longer carried: its new result needs its own evidence.
      const reopened = match.carried && sent.state !== "done";
      Object.assign(match, sent, { id: match.id, hint: sent.hint ?? match.hint });
      if (reopened || sent.evidence) match.carried = false;
    }
    return this.snapshot();
  }

  /** Marks every row with evidence the run's own calls do not show as pending again. Returns them. */
  reconcile(messages: ModelMessage[]): LedgerRow[] {
    const shown = toolText(messages);
    const unbacked: LedgerRow[] = [];
    for (const row of this.rows) {
      if (row.state !== "done" || row.carried) continue;
      if (backed(row.evidence, shown)) continue;
      row.state = "pending";
      row.reason = "Marked done without evidence this run shows.";
      unbacked.push({ ...row });
    }
    return unbacked;
  }

  /** What is not finished, as the report lists it. */
  remaining(): TaskCompletion["remaining"] {
    return this.rows
      .filter((row) => row.state !== "done" && row.state !== "skipped")
      .map((row) => ({ requirement: row.requirement, reason: row.reason || (row.state === "pending" ? "Not done yet." : row.state) }));
  }
}

/** Whether an evidence ref appears in what this run's tools were called with or returned. */
export function backed(evidence: LedgerEvidence | undefined, shown: string): boolean {
  if (!evidence) return false;
  return shown.includes(evidence.ref.toLowerCase());
}

/** Tool calls and results as lower-case text, leaving out the ledger's own calls. */
function toolText(messages: ModelMessage[]): string {
  const parts: string[] = [];
  for (const message of messages) {
    if (typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type === "tool-call" && part.toolName !== ledgerTool) parts.push(JSON.stringify(part.input ?? ""));
      else if (part.type === "tool-result" && part.toolName !== ledgerTool) parts.push(JSON.stringify(part.output ?? ""));
    }
  }
  return parts.join("\n").toLowerCase();
}

/** The report for a run with a ledger: its rows decide what remains and whether it completed. */
export function withLedger(report: TaskCompletion, ledger: Ledger): TaskCompletion {
  if (!ledger.size) return report;
  const remaining = ledger.remaining();
  const extra = report.remaining.filter((item) => !remaining.some((row) => row.requirement === item.requirement));
  const open = [...remaining, ...extra];
  const outcome = open.length ? (report.outcome === "completed" ? "partial" : report.outcome) : report.outcome;
  return { ...report, outcome, remaining: open };
}

/** The text a finished run ends with: the report, then its ledger for the app to read. */
export function ledgerSuffix(ledger: Ledger): string {
  if (!ledger.size) return "";
  const rows = ledger.snapshot().map(({ carried: _carried, ...row }) => row);
  return `\n\n${marker}${JSON.stringify(rows).replace(/</g, "\\u003c")}-->`;
}

/** The ledger a resume turn carries in its prompt, or none. */
export function ledgerFromPrompt(prompt: string): LedgerRow[] {
  const start = prompt.indexOf(marker);
  if (start < 0) return [];
  const end = prompt.indexOf("-->", start);
  if (end < 0) return [];
  try {
    const parsed = z.array(rowSchema.extend({ id: z.string() })).safeParse(JSON.parse(prompt.slice(start + marker.length, end)));
    return parsed.success ? parsed.data : [];
  } catch {
    return [];
  }
}

/** The finish issue for rows marked done that nothing in the run backs. */
export function ledgerIssue(unbacked: LedgerRow[]): string | undefined {
  if (!unbacked.length) return undefined;
  return `These parts are marked done but nothing this run did shows it (${unbacked.map((row) => `"${row.requirement.slice(0, 80)}"`).join("; ")}). ` +
    `Do each, or give it evidence with ${ledgerTool}: the address of a page you kept, a note or file you wrote, or a receipt a tool returned. ` +
    "If a part could not be done, mark it failed or blocked with the reason.";
}

/** The text with its ledger comment taken off, as a chat's history carries it. */
export function stripLedger(text: string): string {
  const start = text.indexOf(marker);
  if (start < 0) return text;
  const end = text.indexOf("-->", start);
  return (text.slice(0, start) + (end < 0 ? "" : text.slice(end + 3))).trim();
}

/**
 * The turn that carries on from a ledger: finished rows stay finished, open ones are the work.
 * The same text the app sends for a resume (src/features/agents/checkpoints/ledger.ts), for the
 * evals, which resume failed tasks through the API.
 */
export function resumeText(rows: LedgerRow[], only?: string[]): string {
  const isOpen = (row: LedgerRow) => row.state !== "done" && row.state !== "skipped";
  const work = rows.filter((row) => isOpen(row) && (!only || only.includes(row.id)));
  const waiting = rows.filter((row) => isOpen(row) && !work.includes(row));
  const done = rows.filter((row) => row.state === "done");
  const lines = ["Continue the request above. Resume the open items; the finished ones stay finished."];
  if (done.length)
    lines.push("", "Done, so don't redo them. If one's evidence is a page, open it once to confirm it still shows the result:",
      ...done.map((row) => `- ${row.requirement}${row.evidence ? ` (${row.evidence.kind}: ${row.evidence.ref})` : ""}`));
  lines.push("", "Open, to work now:", ...work.map((row) => `- ${row.requirement}${row.reason ? ` (last time: ${row.reason})` : ""}${row.hint ? `. The person says: ${row.hint}` : ""}`));
  if (waiting.length) lines.push("", "Open, leave alone for now:", ...waiting.map((row) => `- ${row.requirement}`));
  const kept = rows.map(({ carried: _carried, ...row }) => row);
  return lines.join("\n") + `\n\n${marker}${JSON.stringify(kept).replace(/</g, "\\u003c")}-->`;
}
