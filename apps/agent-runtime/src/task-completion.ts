import type { ModelMessage } from "ai";
import { z } from "zod";

export const taskCompletionTool = "agent_finish";
const remainingSchema = z.array(z.object({
  requirement: z.string().trim().min(1).max(1000),
  reason: z.string().trim().min(1).max(1000),
})).max(20);
/** The recorded report: the tool returns it with its summary filled in. */
export const taskCompletionSchema = z.object({
  outcome: z.enum(["completed", "partial", "blocked"]),
  summary: z.string().trim().min(1).max(12000),
  remaining: remainingSchema,
});
export type TaskCompletion = z.infer<typeof taskCompletionSchema>;
/** What the model sends: summary may be left out when the answer was just given as text. */
export const taskCompletionInputSchema = taskCompletionSchema.extend({
  summary: z.string().trim().max(12000).optional(),
});
type TaskCompletionInput = z.infer<typeof taskCompletionInputSchema>;

/**
 * The text answer agent_finish confirms: the latest model response written as plain text,
 * looking past earlier agent_finish calls (a refused report sent again). A response that
 * called other tools is narration, not an answer, so there is none.
 */
export function lastTextAnswer(messages: ReadonlyArray<ModelMessage>): string {
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]!;
    if (message.role !== "assistant") continue;
    const content = typeof message.content === "string" ? [{ type: "text" as const, text: message.content }] : message.content;
    if (content.some((part) => part.type === "tool-call" && part.toolName !== taskCompletionTool)) return "";
    const text = content.map((part) => part.type === "text" ? part.text : "").join("").trim();
    if (text) return isProgressNote(text) ? "" : text;
  }
  return "";
}

/** The report with its summary filled from the text answer when the model left it out. */
export function withAnswerSummary(input: TaskCompletionInput, messages: ReadonlyArray<ModelMessage>): TaskCompletion | undefined {
  const summary = input.summary?.trim() || lastTextAnswer(messages).slice(0, 12_000);
  return summary ? { ...input, summary } : undefined;
}

export const summaryMissingNote = "agent_finish had no summary and there is no answer written just before it. " +
  "Call agent_finish again with your full answer to the person in summary.";
type CompletionPart = { type: string; toolName?: string; toolCallId?: string; output?: unknown };
type CompletionStep = { content: CompletionPart[] };

/** Only the final, standalone terminal call reports the outcome of this task.
 * Earlier reports cannot survive further work or newly received steering. */
export function finalTaskCompletion(steps: CompletionStep[], executedResults: CompletionPart[] = []): TaskCompletion | undefined {
  const last = steps.at(-1);
  if (!last) return undefined;
  const toolParts = last.content.filter(part => part.type.startsWith("tool-"));
  if (toolParts.some(part => part.toolName !== taskCompletionTool || part.type === "tool-error")) return undefined;
  let results = toolParts.filter(part => part.type === "tool-result");
  // The pinned WorkflowAgent exposes executed raw outputs in toolResults;
  // step.content contains the model's calls before those tools execute.
  // Accept only the receipt paired with the sole final completion call.
  if (!results.length) {
    const calls = toolParts.filter(part => part.type === "tool-call");
    if (calls.length !== 1 || !calls[0]!.toolCallId) return undefined;
    results = executedResults.filter(part => part.toolCallId === calls[0]!.toolCallId);
    if (results.some(part => part.type !== "tool-result" || part.toolName !== taskCompletionTool)) return undefined;
  }
  if (results.length !== 1) return undefined;
  const report = taskCompletionSchema.safeParse(results[0]!.output);
  return report.success ? report.data : undefined;
}

export function taskCompletionOutcome(report: TaskCompletion | undefined) {
  if (!report) return {
    status: "incomplete" as const,
    error_code: "task_outcome_unreported",
    error_message: "The agent stopped without confirming the requested outcome.",
  };
  if (report.outcome === "completed" && report.remaining.length === 0) return { status: "success" as const };
  return {
    status: "incomplete" as const,
    error_code: report.outcome === "blocked" ? "task_blocked" : "task_incomplete",
    error_message: report.remaining.length
      ? report.remaining.map(item => `${item.requirement}: ${item.reason}`).join("; ")
      : "The agent reported that the requested work is not complete.",
  };
}

export function taskCompletionText(report: TaskCompletion): string {
  return report.summary + (report.remaining.length
    ? "\n\nStill incomplete:\n" + report.remaining.map(item => `- ${item.requirement}: ${item.reason}`).join("\n")
    : "");
}

/** Sent once when a run that used its tools answers without agent_finish. */
export const finishMissingNote = "You answered in text but did not report the task's outcome. Call agent_finish now, on its own, " +
  "with only outcome and remaining, set honestly: leave summary out, since your answer above is already the summary and the person has read it. " +
  "Don't repeat the answer or call other tools first unless the answer is missing something.";

/** An announced next move: "Let me click…", "I'll open…", "Now I need to…". */
const announcedMove = /\b(let me|let's|i'll|i will|i'm going to|i am going to|i need to|i should|i'm trying|i'll try|now i|next,? i)\b/i;

/**
 * Whether plain text is the turn's progress line rather than an answer: "Last: … Next: …", or a
 * short note that announces the next move and makes no call ("The popup is still showing. Let me
 * try the link directly."). A run ended on such a line with five sites left (Odysseys, Oct 20).
 */
export function isProgressNote(text: string): boolean {
  const head = text.trim().slice(0, 600);
  if (head.length < 400 && /^(\*\*)?(Last|Have|Next)\b/i.test(head) && /\bNext\b/i.test(head)) return true;
  if (head.length >= 500 || head.includes("?") || /^#|\n\s*[-*|#]/.test(head)) return false;
  const sentences = head.split(/(?<=[.!:])\s+/).filter(Boolean);
  return announcedMove.test(sentences.at(-1) ?? "");
}

/**
 * What a run that never called agent_finish answered: its fullest plain-text answer since its last
 * tool call, not just its last words. Asked once to confirm, a model replied "My full answer is
 * already provided above" with a short recap, and the recap replaced a complete shortlist as the
 * run's answer (Odysseys, October 20).
 */
export function closingText(steps: ReadonlyArray<{ text?: string; content: ReadonlyArray<{ type: string }> }>): string {
  let lastCall = -1;
  steps.forEach((step, index) => {
    if (step.content.some((part) => part.type === "tool-call")) lastCall = index;
  });
  const answers = steps.slice(lastCall + 1).map((step) => step.text?.trim() ?? "").filter((text) => text && !isProgressNote(text));
  const fullest = answers.reduce((best, text) => (text.length > best.length ? text : best), "");
  if (fullest) return fullest;
  for (let index = steps.length - 1; index >= 0; index--) {
    const text = steps[index]?.text?.trim();
    if (text) return text;
  }
  return "";
}

export const progressNoteNudge = "That was your progress line, not an answer. Make the tool calls for the next step now, " +
  "or, if the work is done, call agent_finish with the full answer.";

export const taskCompletionInstructions = `A reply that needs no action, such as a greeting or a question you can answer from what you already know, ends with your plain text answer and no tool calls. Finish every task with one standalone agent_finish call, with no other tool calls in that response. Its summary is your final answer to the user: give every table, list and detail the request asks for in full there, never pointing to an earlier message or saying it is above. If you just wrote that answer as text in your previous response, leave summary out instead of repeating it; that text becomes the summary. Choose completed only when every requested deliverable is actually complete and verified; set remaining to an empty list. Choose blocked when required access, information, permission, or capability is missing; choose partial when only part of the requested work was achieved. For blocked or partial outcomes, list each unfinished requirement and its reason. Explaining why you cannot do the work does not complete the user's requested deliverable. Do not issue a completed report just because the model response is ending. This report cannot replace tool receipts.`;
