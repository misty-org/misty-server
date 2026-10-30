import { FatalError, RetryableError } from "workflow";
import { ControlPlaneError } from "./control-plane-error.js";
import { classifyMCPTransportError } from "./mcp-errors.js";

/** Refusals the run stops on with the reason the person can act on. */
const refusals = new Set(["usage_limit_reached", "email_unverified", "model_key_needed", "model_unsupported", "model_base_url_invalid"]);

/** Step failures retry only for transient control-plane or transport errors. */
export function rethrowStepError(error: unknown): never {
  if (error instanceof ControlPlaneError) {
    if (error.code === "agent_model_turn_limit" || error.code === "agent_execution_time_limit") throw new FatalError(error.code);
    // Refusals keep their code, so the run fails with the reason the person
    // can act on rather than a generic authorization error.
    if (refusals.has(error.code)) throw new FatalError(error.code);
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

/**
 * A model provider failing on its side (a 5xx, or a rate limit) after the call's own retries.
 * The run waits it out between turns (workflows/space-task-agent.ts) instead of failing: on
 * October 21 a free endpoint's one "Internal Server Error" ended a run 33 seconds in.
 */
export function providerOutage(error: unknown): boolean {
  const normalized = runtimeErrorDescription(error).toLowerCase();
  return normalized.includes("gatewayinternalservererror") || normalized.includes("service temporarily unavailable") ||
    // A stream that went quiet through every retry (src/instance-model.ts): the endpoint hung.
    normalized.includes("model_stream_stalled") ||
    ((normalized.includes("dostreamstep") || normalized.includes("ai_apicallerror")) && /internal server error|bad gateway|service unavailable|gateway timeout|too many requests|rate limit/.test(normalized));
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
  if (normalized.includes("model_key_needed")) {
    return { code: "model_key_needed", message: "Add a model key in Settings → Models." };
  }
  if (normalized.includes("model_unsupported")) {
    return { code: "model_unsupported", message: "The chosen model can't do this. Pick another in Settings → Models." };
  }
  if (normalized.includes("model_base_url_invalid")) {
    return { code: "model_base_url_invalid", message: "The model's base URL can't be used. Check it in Settings → Models." };
  }
  if (normalized.includes("timeout") || normalized.includes("timed out") || normalized.includes("aborted")) {
    return { code: "agent_runtime_timeout", message: "Misty timed out before completing this request." };
  }
  if (normalized.includes("usage_limit_reached") && normalized.includes("too many agent tasks")) {
    return { code: "usage_limit_reached", message: "Too many agent tasks are running at once. Wait for one to finish, then try again." };
  }
  if (normalized.includes("usage_limit_reached") && normalized.includes("starting agent tasks faster")) {
    return { code: "usage_limit_reached", message: "You're starting agent tasks faster than they can run. Wait a minute, then try again." };
  }
  if (normalized.includes("usage_limit_reached")) {
    return { code: "usage_limit_reached", message: "Misty paused here because a rate limit was reached. Try again in a moment." };
  }
  if (normalized.includes("email_unverified")) {
    return { code: "email_unverified", message: "Confirm your email to start using AI. Check your inbox for the link." };
  }
  if ((error instanceof ControlPlaneError && !error.transient) || normalized.includes("authorization_or_state_changed")) {
    return { code: "authorization_or_state_changed", message: "Misty's authorization or run state changed." };
  }
  if (normalized.includes("positive credit balance is required") || normalized.includes("insufficient_quota")) {
    return {
      code: "model_provider_credit_exhausted",
      message: "The model provider says this key has no credit left. Add credit with the provider or use another key in Settings → Models.",
    };
  }
  if (
    normalized.includes("api key has been invalidated") ||
    normalized.includes("invalid api key") ||
    normalized.includes("incorrect api key") ||
    normalized.includes("invalid_api_key")
  ) {
    return {
      code: "model_provider_key_invalid",
      message: "The model provider rejected the API key. Check the key in Settings → Models.",
    };
  }
  // A provider's own 5xx after the step's retries (a free endpoint's "Internal Server Error").
  if (providerOutage(error)) {
    return { code: "model_gateway_unavailable", message: "The model provider is temporarily unavailable. Please try again shortly." };
  }
  // The person sees a generic failure; the reason belongs in the runtime's log, or the failure
  // can never be found (a whole Odysseys run ended this way with nothing logged).
  console.error(`[misty-agent-runtime] unclassified_failure ${runtimeErrorDescription(error).replace(/\s+/g, " ").slice(0, 1_500)}`);
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
