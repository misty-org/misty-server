import { describe, expect, it } from "vitest";
import {
  ProtocolError,
  SdkErrorCode,
  SdkHttpError,
} from "@modelcontextprotocol/client";
import { ControlPlaneError } from "../src/control-plane-error.js";
import {
  classifyMCPTransportError,
  classifyRuntimeError,
  classifyToolCompletion,
  incompleteToolResultText,
  recoverableToolError,
  selectActiveRuntimeToolKeys,
  stopOnRepeatedOrTerminalToolFailure,
  taskCreateInputSchema,
  toolFailureSignature,
} from "../workflows/space-task-agent.js";

describe("runtime error classification", () => {
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
					"invalid_tool_input: target_not_grounded: search or read the exact item first",
					"invalid_tool_input",
				),
			),
		).toEqual({
			code: "invalid_tool_input",
			message: "target_not_grounded: search or read the exact item first",
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

describe("tool completion classification", () => {
  it("uses the MCP catalog as the production tool availability authority", () => {
    const names = {
      context_get: "context.get",
      weather_current: "weather.current",
    };
    const allowed = ["context.get", "weather.current"];
    expect(
      selectActiveRuntimeToolKeys(names, allowed, {
        supported: true,
        advertisedToolNames: ["context.get"],
      }),
    ).toEqual(["context_get"]);
    expect(
      selectActiveRuntimeToolKeys(names, allowed, {
        supported: true,
        advertisedToolNames: allowed,
      }),
    ).toEqual(["context_get", "weather_current"]);
    expect(
      selectActiveRuntimeToolKeys(names, allowed, {
        supported: false,
        advertisedToolNames: [],
      }),
    ).toEqual(["context_get", "weather_current"]);
  });

  it("does not report success after a failed tool action", () => {
    expect(classifyToolCompletion(["tasks_create", "tasks_create"])).toEqual({
      status: "incomplete",
      error_code: "tool_execution_failed",
      error_message: "Could not complete tasks_create.",
    });
    expect(classifyToolCompletion([])).toEqual({ status: "success" });

    const visible = incompleteToolResultText([
      { toolName: "tasks_create", error: "assignee could not be resolved" },
    ]);
    expect(visible).toContain("couldn't fully complete");
    expect(visible).toContain("tasks create");
    expect(visible).not.toContain("success");
  });

  it("only offers task statuses accepted by Misty", () => {
    expect(
      taskCreateInputSchema.safeParse({ title: "Algebra", status: "pending" })
        .success,
    ).toBe(false);
    expect(
      taskCreateInputSchema.safeParse({
        title: "Algebra",
        status: "todo",
        dueAt: "2026-08-19T05:00:00Z",
        dueTimezone: "America/Los_Angeles",
        assigneeUserId: "user_melissa",
      }).success,
    ).toBe(true);
    expect(
      taskCreateInputSchema.safeParse({
        title: "Algebra",
        dueAt: "2024-11-21T19:00:00",
      }).success,
    ).toBe(false);
  });

  it("uses stable call signatures and stops poisoned tool loops", async () => {
    expect(toolFailureSignature("weather.current", { b: 2, a: 1 })).toBe(
      toolFailureSignature("weather.current", { a: 1, b: 2 }),
    );
    const repeated = {
      steps: [
        {
          content: [
            {
              type: "tool-error",
              toolName: "weather_current",
              toolCallId: "call-1",
              input: { location: "Arcadia, CA" },
              error: new Error("tool_service_unavailable"),
            },
          ],
        },
        {
          content: [
            {
              type: "tool-error",
              toolName: "weather_current",
              toolCallId: "call-2",
              input: { location: "Arcadia, CA" },
              error: new Error("tool_service_unavailable"),
            },
          ],
        },
      ],
    };
    expect(
      await stopOnRepeatedOrTerminalToolFailure(repeated as never),
    ).toBe(true);
    expect(
      await stopOnRepeatedOrTerminalToolFailure({
        steps: [
          {
            content: [
              {
                type: "tool-error",
                toolName: "weather_current",
                toolCallId: "call-3",
                input: { location: "Arcadia, CA" },
                error: new Error("tool_unavailable"),
              },
            ],
          },
        ],
      } as never),
    ).toBe(true);
  });
});
