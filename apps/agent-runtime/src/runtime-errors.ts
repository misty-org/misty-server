import { FatalError, RetryableError } from "workflow";
import { ControlPlaneError } from "./control-plane-error.js";
import { classifyMCPTransportError } from "./mcp-errors.js";

/** Step failures retry only for transient control-plane or transport errors. */
export function rethrowStepError(error: unknown): never {
  if (error instanceof ControlPlaneError) {
    if (error.code === "agent_model_turn_limit" || error.code === "agent_execution_time_limit") throw new FatalError(error.code);
    // Usage refusals keep their code, so the run fails with the reason the person
    // can act on rather than a generic authorization error.
    if (error.code === "billing_admission_denied" || error.code === "hosted_ai_limit_reached") throw new FatalError(error.code);
    if (error.transient) {
      throw new RetryableError("Misty's control plane is temporarily unavailable.", {
        retryAfter: error.status === 429 ? 5_000 : 1_000,
      });
    }
    throw new FatalError("authorization_or_state_changed: Misty's authorization or run state changed.");
  }
  const failure = classifyMCPTransportError(error);
  if (failure.transient) throw new RetryableError(failure.message, { retryAfter: failure.retryAfterMs });
  if (failure.recognized) throw new FatalError(failure.message);
  throw error;
}

/** Validation responses the model can correct, without workflow internals. */
export function recoverableToolError(error: unknown): { code: string; message: string } | null {
  if (!(error instanceof ControlPlaneError)) return null;
  if (error.code === "agent_model_turn_limit" || error.code === "agent_execution_time_limit") return null;
  if (![400, 409, 422].includes(error.status)) return null;
  return {
    code: error.code || "invalid_tool_input",
    message: error.message.replace(/^[a-z0-9_]+:\s*/i, "").trim().slice(0, 500),
  };
}

export function classifyRuntimeError(error: unknown): { code: string; message: string } {
  // Workflow step failures may arrive as serialized errors or wrap the
  // provider error in a cause. Inspect diagnostic fields, never raw responses.
  const normalized = runtimeErrorDescription(error).toLowerCase();
  if (normalized.includes("agent_model_turn_limit")) {
    return { code: "agent_model_turn_limit", message: "This run reached its model-turn limit. Review its completed work before starting another request." };
  }
  if (normalized.includes("agent_execution_time_limit")) {
    return { code: "agent_execution_time_limit", message: "This run used its execution-time allowance. Review its completed work before starting another request." };
  }
  if (normalized.includes("timeout") || normalized.includes("timed out") || normalized.includes("aborted")) {
    return { code: "agent_runtime_timeout", message: "Misty timed out before completing this request." };
  }
  if (normalized.includes("billing_admission_denied")) {
    return { code: "billing_admission_denied", message: "Your account's AI usage is paused, so Misty couldn't run this. Check Account usage." };
  }
  if (normalized.includes("hosted_ai_limit_reached")) {
    return { code: "hosted_ai_limit_reached", message: "Your weekly AI agent allowance is fully used. Try again after it resets." };
  }
  if ((error instanceof ControlPlaneError && !error.transient) || normalized.includes("authorization_or_state_changed")) {
    return { code: "authorization_or_state_changed", message: "Misty's authorization or run state changed." };
  }
  if (normalized.includes("positive credit balance is required") || normalized.includes("insufficient_quota")) {
    return {
      code: "model_provider_credit_exhausted",
      message: "Misty's AI provider has no available credit. The server administrator needs to add credit or configure another provider.",
    };
  }
  if (normalized.includes("gatewayinternalservererror") || normalized.includes("service temporarily unavailable")) {
    return { code: "model_gateway_unavailable", message: "Misty's model providers are temporarily unavailable. Please try again shortly." };
  }
  return { code: "agent_runtime_failed", message: "Misty could not complete this request." };
}

function runtimeErrorDescription(error: unknown, depth = 0): string {
  if (depth >= 6 || error == null) return "";
  if (typeof error === "string") return error;
  if (typeof error !== "object") return "";
  const diagnostic = error as { name?: unknown; message?: unknown; cause?: unknown };
  return [
    typeof diagnostic.name === "string" ? diagnostic.name : "",
    typeof diagnostic.message === "string" ? diagnostic.message : "",
    runtimeErrorDescription(diagnostic.cause, depth + 1),
  ].join(" ");
}

export function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  try {
    return JSON.stringify(error);
  } catch {
    return "";
  }
}

/** Public timeline text for a failed call; raw diagnostics stay private. */
export function visibleErrorMessage(error: unknown): string {
  const message = errorText(error).toLowerCase();
  if (message.includes("browser_webview_unavailable")) {
    return "The local browser view is unavailable. Reopen the browser view and retry the task.";
  }
  if (message.includes("desktop_accessibility_required")) {
    return "Allow the running Misty app in System Settings → Privacy & Security → Accessibility, then retry.";
  }
  if (message.includes("desktop_screen_recording_required")) {
    return "Allow the running Misty app in System Settings → Privacy & Security → Screen Recording, then retry.";
  }
  if (message.includes("invalid_tool_input")) return "The tool arguments were invalid.";
  if (message.includes("permission_denied") || message.includes("authorization_or_state_changed")) {
    return "This run is no longer authorized to use that tool.";
  }
  if (message.includes("tool_unavailable") || message.includes("unknown tool")) return "That tool is not available for this run.";
  if (message.includes("conflict")) return "The item changed while Misty was working. Please retry.";
  if (message.includes("rate_limited") || message.includes("too many requests")) {
    return "Misty's tool service is busy. Please try again shortly.";
  }
  if (message.includes("timeout") || message.includes("temporarily unavailable")) {
    return "Misty's tool service is temporarily unavailable.";
  }
  return "The tool could not complete this action.";
}
