/** Transport compatibility envelope. Absence of a result is not confirmation. */
export interface ToolExecutionResponse {
  result?: unknown;
  device_wait?: boolean;
  intervention_wait?: { id: string; action: string; reason: string };
  tool_error?: { code: string; message: string };
}

export interface ToolExecutionContinuation {
  request(attempt: number): Promise<ToolExecutionResponse>;
  device(attempt: number): Promise<boolean>;
  intervention?(attempt: number): Promise<boolean>;
}

/** Misty reported this call as failed without effect: it rejected the call
 * before execution or recorded a definite failure. Unknown write outcomes
 * arrive as an `uncertain` result instead, never as this error. */
export class ToolRejectedError extends Error {
  constructor(readonly code: string, message: string) {
    super(`${code}: ${message}`);
    this.name = "ToolRejectedError";
  }
}

// Transport anomalies where the call may have run despite the failure.
const unverifiedFailureCodes = new Set(["invalid_tool_outcome", "missing_tool_result"]);

export function rejectedWithoutEffect(error: unknown): boolean {
  return error instanceof ToolRejectedError && !unverifiedFailureCodes.has(error.code);
}

/** Engine-independent outcomes for the authoritative transport response. */
export type HarnessToolOutcome =
  | { status: "success"; result: unknown }
  | { status: "failure"; code: string; message: string }
  | { status: "device_required" }
  | { status: "intervention_wait"; wait: NonNullable<ToolExecutionResponse["intervention_wait"]> }
  | { status: "user_intervention_required"; result: unknown }
  | { status: "uncertain"; result: unknown };

export function normalizeToolOutcome(response: ToolExecutionResponse): HarnessToolOutcome {
  if (!response || typeof response !== "object" || Array.isArray(response)) throw new Error("invalid_tool_outcome");
  const hasResult = Object.hasOwn(response, "result") && response.result !== undefined;
  const alternatives = Number(hasResult) + Number(response.device_wait === true) + Number(Boolean(response.tool_error)) + Number(Boolean(response.intervention_wait));
  if (alternatives === 0) throw new Error("missing_tool_result: execution was not confirmed");
  if (alternatives !== 1) throw new Error("invalid_tool_outcome: contradictory execution outcomes");
  if (response.tool_error) {
    if (typeof response.tool_error.code !== "string" || !response.tool_error.code || typeof response.tool_error.message !== "string") throw new Error("invalid_tool_outcome: malformed failure");
    return {status:"failure",...response.tool_error};
  }
  if (response.intervention_wait) {
    const wait = response.intervention_wait;
    if (typeof wait.id !== "string" || !wait.id || typeof wait.reason !== "string" || !["sign_in","account_confirmation","challenge","open_target","review"].includes(wait.action)) throw new Error("invalid_tool_outcome: malformed intervention wait");
    return {status:"intervention_wait",wait};
  }
  if (response.device_wait) return {status:"device_required"};
  const status = response.result && typeof response.result === "object" && "status" in response.result ? response.result.status : undefined;
  if (status === "uncertain" || status === "user_intervention_required") return {status, result:response.result};
  return {status:"success",result:response.result};
}

/** The workflow owns durable hooks; this loop never invents a new effect identity. */
export async function continueToolExecution(continuation: ToolExecutionContinuation): Promise<unknown> {
  for (let attempt = 0; attempt < 64; attempt++) {
    const outcome = normalizeToolOutcome(await continuation.request(attempt));
    switch (outcome.status) {
      case "failure": throw new ToolRejectedError(outcome.code, outcome.message);
      case "intervention_wait":
        if (!continuation.intervention) throw new Error("intervention_adapter_unavailable");
        if (!(await continuation.intervention(attempt))) return {denied:true,reason:"user_action_declined_or_expired",waitId:outcome.wait.id};
        break;
      case "device_required":
        if (!(await continuation.device(attempt))) return {unavailable:true,reason:"device_unavailable"};
        break;
      // The outer agent stops on unresolved intervention and uncertainty. Neither
      // may turn into a new planning attempt for the same consequential action.
      case "user_intervention_required":
      case "uncertain":
      case "success": return outcome.result;
    }
  }
  throw new Error("tool_wait_limit: too many resumptions without completion");
}
