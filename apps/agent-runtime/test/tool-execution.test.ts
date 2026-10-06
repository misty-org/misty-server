import { describe, expect, it, vi } from "vitest";
import {
  continueToolExecution,
  normalizeToolOutcome,
  type ToolExecutionResponse,
} from "../src/tool-execution.js";

describe("durable tool continuations", () => {
  it("rechecks every outcome after repeated device waits", async () => {
    const responses: ToolExecutionResponse[] = [
      { device_wait: true },
      { device_wait: true },
      { result: { messageId: "sent-once" } },
    ];
    const request = vi.fn(async (attempt: number) => responses[attempt]!);
    const device = vi.fn(async () => true);
    expect(await continueToolExecution({ request, device })).toEqual({
      messageId: "sent-once",
    });
    expect(request).toHaveBeenCalledTimes(3);
    expect(device).toHaveBeenCalledTimes(2);
  });
  it("never confirms absent results after a wait", async () => {
    await expect(
      continueToolExecution({
        request: async (attempt) => (attempt ? {} : { device_wait: true }),
        device: async () => true,
      }),
    ).rejects.toThrow("missing_tool_result");
  });
  it("stops immediately on device expiry", async () => {
    const request = vi.fn(async () => ({ device_wait: true }));
    expect(await continueToolExecution({ request, device: async () => false })).toMatchObject({
      unavailable: true,
    });
    expect(request).toHaveBeenCalledTimes(1);
  });
  it("does not suppress errors or accept contradictory waits", async () => {
    for (const response of [
      { tool_error: { code: "revoked", message: "Access revoked" } },
      {
        device_wait: true,
        intervention_wait: { id: "a", action: "sign_in", reason: "Sign in" },
      },
    ]) {
      await expect(
        continueToolExecution({ request: async () => response, device: async () => true }),
      ).rejects.toThrow();
    }
  });
});

it("rejects a wait mixed with a success and malformed wait identities", () => {
  expect(() => normalizeToolOutcome({ device_wait: true, result: { sent: true } })).toThrow(
    "contradictory",
  );
  expect(() =>
    normalizeToolOutcome({ intervention_wait: { id: "", action: "sign_in", reason: "Sign in" } }),
  ).toThrow("malformed");
  expect(normalizeToolOutcome({ result: { status: "uncertain", effectId: "send" } }).status).toBe(
    "uncertain",
  );
  expect(
    normalizeToolOutcome({ result: { status: "user_intervention_required", action: "sign_in" } })
      .status,
  ).toBe("user_intervention_required");
});

it("continues the same request through device and repeated user-action waits", async () => {
  const replies: ToolExecutionResponse[] = [
    { device_wait: true },
    { intervention_wait: { id: "login", action: "sign_in", reason: "Sign in" } },
    { intervention_wait: { id: "account", action: "account_confirmation", reason: "Check account" } },
    { result: { ready: true, requiresFreshInspection: true } },
  ];
  const intervention = vi.fn(async () => true);
  expect(
    await continueToolExecution({
      request: async (attempt) => replies[attempt]!,
      device: async () => true,
      intervention,
    }),
  ).toMatchObject({ ready: true });
  expect(intervention).toHaveBeenCalledTimes(2);
  const request = vi.fn(async () => ({
    intervention_wait: { id: "login", action: "sign_in", reason: "Sign in" },
  }));
  expect(
    await continueToolExecution({ request, device: async () => true, intervention: async () => false }),
  ).toMatchObject({ denied: true });
  expect(request).toHaveBeenCalledTimes(1);
});
