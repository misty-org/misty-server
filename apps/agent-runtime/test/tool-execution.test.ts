import { describe, expect, it, vi } from "vitest";
import {
  continueToolExecution,
  normalizeToolOutcome,
  type ToolExecutionResponse,
} from "../src/tool-execution.js";

describe("durable tool continuations", () => {
  it("rechecks every outcome after alternating approval and device waits", async () => {
    const responses: ToolExecutionResponse[] = [
      { approval: { id: "approval-1", state: "pending" } },
      { device_wait: true },
      { approval: { id: "approval-2", state: "pending" } },
      { device_wait: true },
      { result: { messageId: "sent-once" } },
    ];
    const request = vi.fn(async (attempt: number) => responses[attempt]!);
    const approval = vi.fn(async () => true);
    const device = vi.fn(async () => true);
    expect(await continueToolExecution({ request, approval, device })).toEqual({
      messageId: "sent-once",
    });
    expect(request).toHaveBeenCalledTimes(5);
    expect(approval).toHaveBeenCalledTimes(2);
    expect(device).toHaveBeenCalledTimes(2);
  });
  it("never confirms absent results after approval", async () => {
    await expect(
      continueToolExecution({
        request: async (attempt) =>
          attempt ? {} : { approval: { id: "a", state: "pending" } },
        approval: async () => true,
        device: async () => true,
      }),
    ).rejects.toThrow("missing_tool_result");
  });
  it("stops immediately on denial or device expiry", async () => {
    const request = vi.fn(async () => ({ device_wait: true }));
    expect(
      await continueToolExecution({
        request,
        approval: async () => true,
        device: async () => false,
      }),
    ).toMatchObject({ unavailable: true });
    expect(request).toHaveBeenCalledTimes(1);
  });
  it("does not suppress errors or accept contradictory waits", async () => {
    for (const response of [
      { tool_error: { code: "revoked", message: "Access revoked" } },
      { device_wait: true, approval: { id: "a", state: "pending" } },
    ]) {
      await expect(
        continueToolExecution({
          request: async () => response,
          approval: async () => true,
          device: async () => true,
        }),
      ).rejects.toThrow();
    }
  });
});

it("rejects a wait mixed with a success and malformed approval identities", () => {
  expect(()=>normalizeToolOutcome({approval:{id:"a",state:"pending"},result:{sent:true}})).toThrow("contradictory");
  expect(()=>normalizeToolOutcome({approval:{id:"",state:"pending"}})).toThrow("malformed");
  expect(normalizeToolOutcome({result:{status:"uncertain",effectId:"send"}}).status).toBe("uncertain");
  expect(normalizeToolOutcome({result:{status:"user_intervention_required",action:"sign_in"}}).status).toBe("user_intervention_required");
});

it("continues the same request through approval, device and repeated user-action waits", async () => {
  const replies: ToolExecutionResponse[] = [
    {approval:{id:"review",state:"pending"}}, {device_wait:true},
    {intervention_wait:{id:"login",action:"sign_in",reason:"Sign in"}},
    {intervention_wait:{id:"account",action:"account_confirmation",reason:"Check account"}},
    {result:{ready:true,requiresFreshInspection:true}},
  ];
  const intervention=vi.fn(async()=>true);
  expect(await continueToolExecution({request:async(attempt)=>replies[attempt]!,approval:async()=>true,device:async()=>true,intervention})).toMatchObject({ready:true});
  expect(intervention).toHaveBeenCalledTimes(2);
  const request=vi.fn(async()=>({intervention_wait:{id:"login",action:"sign_in",reason:"Sign in"}}));
  expect(await continueToolExecution({request,approval:async()=>true,device:async()=>true,intervention:async()=>false})).toMatchObject({denied:true});
  expect(request).toHaveBeenCalledTimes(1);
});
