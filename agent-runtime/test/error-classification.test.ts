import { describe, expect, it } from "vitest";
import { ControlPlaneError } from "../src/control-plane-error.js";
import {
  classifyRuntimeError,
  classifyToolCompletion,
  incompleteToolResultText,
  recoverableToolError,
  taskCreateInputSchema,
} from "../workflows/space-task-agent.js";

describe("runtime error classification", () => {
  it("retries only transient control-plane responses", () => {
    expect(new ControlPlaneError(503, "unavailable").transient).toBe(true);
    expect(new ControlPlaneError(429, "limited").transient).toBe(true);
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
  });
});

describe("tool completion classification", () => {
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
});
