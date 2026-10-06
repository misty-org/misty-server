import { describe, expect, it } from "vitest";
import {
  ProtocolError,
  SdkErrorCode,
  SdkHttpError,
} from "@modelcontextprotocol/client";
import { ControlPlaneError } from "../src/control-plane-error.js";
import { classifyMCPTransportError } from "../src/mcp-errors.js";
import { classifyRuntimeError, recoverableToolError, visibleErrorMessage } from "../src/runtime-errors.js";
import { classifyToolOutcome, incompleteToolResultText, stoppedAtModelTurnLimit, toolFailureSignature } from "../src/tool-outcomes.js";

describe("runtime error classification", () => {
  it("preserves a safe native browser failure without exposing its raw details", () => {
    expect(visibleErrorMessage(new Error("browser_webview_unavailable: private diagnostic details"))).toBe(
      "The local browser view is unavailable. Reopen the browser view and retry the task.",
    );
    expect(visibleErrorMessage(new Error("desktop_accessibility_required: private diagnostic"))).toBe(
      "Allow the running Misty app in System Settings → Privacy & Security → Accessibility, then retry.",
    );
    expect(visibleErrorMessage(new Error("desktop_screen_recording_required: private diagnostic"))).toBe(
      "Allow the running Misty app in System Settings → Privacy & Security → Screen Recording, then retry.",
    );
    expect(visibleErrorMessage(new Error("unclassified private diagnostic"))).toBe(
      "The tool could not complete this action.",
    );
  });
  it("does not treat tool calls at the step limit as a finished answer", () => {
    expect(stoppedAtModelTurnLimit(20, "tool-calls")).toBe(true);
    expect(stoppedAtModelTurnLimit(20, "length")).toBe(true);
    expect(stoppedAtModelTurnLimit(20, "stop")).toBe(false);
    expect(stoppedAtModelTurnLimit(19, "tool-calls")).toBe(false);
  });
  it("stops on a durable model budget limit instead of retrying it as invalid input", () => {
    const error = new ControlPlaneError(422, "agent_model_turn_limit", "agent_model_turn_limit");
    expect(error.transient).toBe(false);
    expect(recoverableToolError(error)).toBeNull();
    expect(classifyRuntimeError(error).code).toBe("agent_model_turn_limit");
    expect(classifyRuntimeError(new Error("agent_model_turn_limit")).code).toBe("agent_model_turn_limit");
  });
  it("treats execution-time exhaustion as a terminal budget outcome", async () => {
    const error = new ControlPlaneError(422, "agent_execution_time_limit", "agent_execution_time_limit");
    expect(error.transient).toBe(false);
    expect(recoverableToolError(error)).toBeNull();
    expect(classifyRuntimeError(error).code).toBe("agent_execution_time_limit");
    expect(classifyToolOutcome({ success: false, error: error.message, readOnly: true, rejected: true }).kind).toBe("stop");
  });
  it("retries only transient control-plane responses", () => {
    expect(new ControlPlaneError(503, "unavailable").transient).toBe(true);
    expect(new ControlPlaneError(429, "limited").transient).toBe(true);
    expect(
      new ControlPlaneError(
        429,
        "hosted_ai_limit_reached",
        "hosted_ai_limit_reached",
      ).transient,
    ).toBe(false);
    expect(new ControlPlaneError(403, "revoked").transient).toBe(false);
  });

  it("gives timeouts and authorization changes stable public codes", () => {
    expect(
      classifyRuntimeError(new DOMException("timed out", "TimeoutError")).code,
    ).toBe("agent_runtime_timeout");
    expect(
      classifyRuntimeError(new ControlPlaneError(403, "grant removed")).code,
    ).toBe("authorization_or_state_changed");
    expect(classifyRuntimeError(new Error("provider failed")).code).toBe(
      "agent_runtime_failed",
    );
    expect(classifyRuntimeError(new Error("hosted_ai_limit_reached"))).toEqual({
      code: "hosted_ai_limit_reached",
      message:
        "Your weekly AI agent allowance is fully used. Try again after it resets.",
    });
    expect(
      classifyRuntimeError(
        Object.assign(
          new Error(
            "Service temporarily unavailable. Please try again shortly.",
          ),
          {
            name: "GatewayInternalServerError",
          },
        ),
      ),
    ).toEqual({
      code: "model_gateway_unavailable",
      message:
        "Misty's model providers are temporarily unavailable. Please try again shortly.",
    });
  });

  it.each([
    new Error("A positive credit balance is required for all requests, including BYOK."),
    { name: "GatewayInternalServerError", message: "A positive credit balance is required for all requests, including BYOK." },
    new Error("Workflow step failed", { cause: { name: "GatewayInternalServerError", message: "A positive credit balance is required for all requests, including BYOK." } }),
    "insufficient_quota",
  ])("preserves provider credit exhaustion through workflow errors", (error) => {
    expect(classifyRuntimeError(error)).toEqual({
      code: "model_provider_credit_exhausted",
      message: "Misty's AI provider has no available credit. The server administrator needs to add credit or configure another provider.",
    });
  });

  it("recognizes serialized gateway outages without returning private provider data", () => {
    expect(classifyRuntimeError({ name: "GatewayInternalServerError", message: "Service temporarily unavailable", response: "private provider payload" })).toEqual({
      code: "model_gateway_unavailable",
      message: "Misty's model providers are temporarily unavailable. Please try again shortly.",
    });
    const circular = { message: "unexpected failure", cause: undefined as unknown };
    circular.cause = circular;
    expect(classifyRuntimeError(circular).code).toBe("agent_runtime_failed");
  });

  it("returns correctable validation errors to the model without workflow internals", () => {
    expect(
      recoverableToolError(
        new ControlPlaneError(
          422,
          "invalid_tool_input: dueAt must be an ISO 8601 date or timestamp",
          "invalid_tool_input",
        ),
      ),
    ).toEqual({
      code: "invalid_tool_input",
      message: "dueAt must be an ISO 8601 date or timestamp",
    });
    expect(
      recoverableToolError(new ControlPlaneError(403, "membership revoked")),
    ).toBeNull();
		expect(
			recoverableToolError(
				new ControlPlaneError(
					422,
					"invalid_tool_input: pass space for this change; available: \"Personal\" (space_1)",
					"invalid_tool_input",
				),
			),
		).toEqual({
			code: "invalid_tool_input",
			message: "pass space for this change; available: \"Personal\" (space_1)",
		});
  });

  it("never retries permanent MCP protocol or authorization failures", () => {
    expect(
      classifyMCPTransportError(
        new ProtocolError(-32601, 'unknown tool "weather.current"'),
      ),
    ).toMatchObject({
      code: "tool_unavailable",
      transient: false,
      recognized: true,
    });
    expect(
      classifyMCPTransportError(
        new SdkHttpError(
          SdkErrorCode.ClientHttpForbidden,
          "forbidden",
          { status: 403 },
        ),
      ),
    ).toMatchObject({ code: "permission_denied", transient: false });
  });

  it("retries only transient MCP HTTP failures with a bounded delay", () => {
    expect(
      classifyMCPTransportError(
        new SdkHttpError(
          SdkErrorCode.ClientHttpFailedToOpenStream,
          "limited",
          { status: 429 },
        ),
      ),
    ).toMatchObject({
      code: "rate_limited",
      transient: true,
      retryAfterMs: 5_000,
    });
  });
});

describe("tool failure reporting", () => {
  it("names the failed action without claiming success", () => {
    const visible = incompleteToolResultText([
      { toolName: "tasks_create", error: "assignee could not be resolved" },
    ]);
    expect(visible).toContain("couldn't fully complete");
    expect(visible).toContain("tasks create");
    expect(visible).not.toContain("success");
  });

  it("uses stable call signatures", () => {
    expect(toolFailureSignature("weather.current", { b: 2, a: 1 })).toBe(
      toolFailureSignature("weather.current", { a: 1, b: 2 }),
    );
  });
});
