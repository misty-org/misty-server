import { describe, expect, it } from "vitest";
import { ControlPlaneError } from "../src/control-plane-error.js";
import { classifyRuntimeError } from "../workflows/space-task-agent.js";

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
});
