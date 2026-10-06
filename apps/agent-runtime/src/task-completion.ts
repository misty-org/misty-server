import { z } from "zod";

export const taskCompletionTool = "misty_finish_task";
export const taskCompletionSchema = z.object({
  outcome: z.enum(["completed", "partial", "blocked"]),
  summary: z.string().trim().min(1).max(12000),
  remaining: z.array(z.object({
    requirement: z.string().trim().min(1).max(1000),
    reason: z.string().trim().min(1).max(1000),
  })).max(20),
});
export type TaskCompletion = z.infer<typeof taskCompletionSchema>;
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

export const taskCompletionInstructions = `A reply that needs no action, such as a greeting or a question you can answer from what you already know, ends with your plain text answer and no tool calls. Finish every task with one standalone misty_finish_task call, with no other tool calls in that response. Its summary is your final answer to the user. Choose completed only when every requested deliverable is actually complete and verified; set remaining to an empty list. Choose blocked when required access, information, permission, or capability is missing; choose partial when only part of the requested work was achieved. For blocked or partial outcomes, list each unfinished requirement and its reason. Explaining why you cannot do the work does not complete the user's requested deliverable. Do not issue a completed report just because the model response is ending. This report cannot replace tool receipts.`;
