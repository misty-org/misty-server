import { errorText } from "./runtime-errors.js";

export const executionInstructions = `Execution contract:
- Treat a concrete imperative request as work to perform now, not as a request for instructions or a proposal.
- Privately break multi-part requests into the necessary steps and continue until every requested action and deliverable is complete or a real blocker prevents progress.
- When creating or updating an artifact with generated content, compose the complete final content before the write. Check every explicit constraint—including title, format, count, length, and requested sections—and send the full result in the tool call. Never create a placeholder, outline, partial draft, or empty shell when the user requested finished content.
- A tool error means that call had no effect. Read it, correct the arguments or choose another tool, and continue. Calls reported as tool_not_attempted never ran; call them again if they are still needed.
- Do not ask the user to restate, plan, create subtasks, or say “finish” when the request already contains enough detail. Ask one focused clarification only when a missing choice would materially change the result.
- If any requested part cannot be completed, say exactly what remains incomplete and why.`;

/** Why a returned result is not a confirmed effect, or "" when it is. */
export function unconfirmedToolResultReason(output: unknown): string {
  if (output === undefined) return "The requested action returned no confirmed result.";
  if (!output || typeof output !== "object") return "";
  const result = output as Record<string, unknown>;
  const toolError = result.tool_error as { message?: unknown } | undefined;
  if (result.status === "failure" && typeof toolError?.message === "string" && toolError.message.trim())
    return toolError.message.slice(0, 500);
  if (result.status === "uncertain") return "The action may have happened, but its outcome could not be verified.";
  if (["failure", "device_required", "user_intervention_required"].includes(String(result.status)))
    return "The requested action has not completed.";
  if (result.denied === true) return "The requested action was not approved.";
  if (result.unavailable === true) return "The device required for this action was unavailable.";
  return "";
}

export type ToolOutcome =
  | { kind: "confirmed" }
  /**
   * The run hands off to the user: a screen Misty opens, or an app card they
   * answer. The desktop continues the conversation once it is done.
   */
  | { kind: "handoff"; reason: string }
  /** The call had no effect; the model sees why and plans again. */
  | { kind: "retry"; reason: string }
  /** The run stops: the outcome needs the user, or the run lost its authority. */
  | { kind: "stop"; reason: string };

const runEndingCodes = ["authorization_or_state_changed", "agent_execution_time_limit", "agent_model_turn_limit", "permission_denied"];

const handoffDefaults: Record<string, string> = {
  screen_requested: "Misty is opening a screen and will continue this conversation with it attached.",
  waiting_for_user: "Connect the app with the card above. Misty continues once it's connected.",
  awaiting_approval: "An action is waiting for your approval above. Misty continues after you decide.",
  // The agent asked structured questions; the conversation continues with the answers.
  awaiting_answer: "Answer the question above and Misty continues.",
  // Plan mode ends its turn with a plan for the user to run, edit or refine.
  plan_proposed: "Review the plan above. Run it, edit it, or keep planning.",
};

/** The user-facing message for a handoff result, or "" when the output is not one. */
export function handoffMessage(output: unknown): string {
  if (!output || typeof output !== "object") return "";
  const result = output as Record<string, unknown>;
  const fallback = handoffDefaults[String(result.status)];
  if (!fallback) return "";
  const text = result.status === "screen_requested" ? result.message : result.user_message;
  return typeof text === "string" && text.trim() ? text.slice(0, 300) : fallback;
}

/** The run lost its authority or budget, or its tool sequence already stopped. */
export function endsRun(message: string): boolean {
  const lower = message.toLowerCase();
  return lower.startsWith("tool_sequence_stopped") || runEndingCodes.some((code) => lower.includes(code));
}

/**
 * Uncertain, denied, unavailable and waiting results are durable decisions for
 * the user. Errors Misty reports for a call it rejected, and any failed read,
 * return to the model. A write whose outcome is unknown stops the run so it is
 * never repeated under a new call ID.
 */
export function classifyToolOutcome(input: { success: boolean; output?: unknown; error?: unknown; readOnly: boolean; rejected: boolean }): ToolOutcome {
  if (input.success) {
    const handoff = handoffMessage(input.output);
    if (handoff) return { kind: "handoff", reason: handoff };
    const reason = unconfirmedToolResultReason(input.output);
    if (!reason) return { kind: "confirmed" };
    const status = (input.output as Record<string, unknown> | undefined)?.status;
    return status === "failure" ? { kind: "retry", reason } : { kind: "stop", reason };
  }
  const message = errorText(input.error);
  if (!endsRun(message) && (input.rejected || input.readOnly)) return { kind: "retry", reason: message };
  return { kind: "stop", reason: message };
}

function stableToolValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableToolValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, item]) => [key, stableToolValue(item)]),
    );
  }
  return value;
}

export function toolFailureSignature(toolName: string, input: unknown): string {
  let encoded = "";
  try {
    encoded = JSON.stringify(stableToolValue(input));
  } catch {
    encoded = "[unserializable]";
  }
  return `${toolName}:${encoded}`;
}

export function incompleteToolResultText(failures: Array<{ toolName: string; error: string }>): string {
  const details = failures
    .slice(0, 3)
    .map(({ toolName, error }) => `${toolName.replace(/[._]/g, " ")}: ${error.trim().slice(0, 300) || "The action could not be completed."}`)
    .join("; ");
  return `I couldn't fully complete that request. ${details || "A required action failed."}`;
}

export function stoppedAtModelTurnLimit(stepCount: number, finishReason: string, limit = 20): boolean {
  return stepCount >= limit && finishReason !== "stop";
}

export function unfinishedModelResult(aborted: boolean, stepCount: number, finishReason: string, limit = 20): { code: string; message: string } | null {
  // WorkflowAgent can resolve an aborted stream with the last completed step's
  // finish reason. A normal return (or earlier text) is not completion evidence.
  if (aborted) {
    return { code: "agent_runtime_interrupted", message: "This run stopped before finishing. Review its completed work before starting another request." };
  }
  if (stoppedAtModelTurnLimit(stepCount, finishReason, limit)) {
    return { code: "agent_model_turn_limit", message: "This run reached its model-turn limit before finishing. Review its completed work before starting another request." };
  }
  if (finishReason === "stop" && stepCount > 0) return null;
  if (finishReason === "length") {
    return { code: "agent_response_incomplete", message: "The model reached its response limit before finishing. Review the partial result before continuing." };
  }
  return { code: "agent_response_incomplete", message: "The model did not finish this request. Review its completed work before starting another request." };
}

export function finalText(steps: Array<{ text?: string }>): string {
  for (let index = steps.length - 1; index >= 0; index -= 1) {
    const text = steps[index]?.text?.trim();
    if (text) return text;
  }
  return "";
}
