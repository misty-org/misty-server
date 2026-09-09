import { beforeEach, expect, it, vi } from "vitest";
const request = vi.hoisted(() => vi.fn());
vi.mock("../src/control-plane.js", () => ({ controlPlaneRequest: request }));
import { startWithReceipt } from "../src/start-receipt.js";

const input = { mistyRunId: "invocation-test", controlPlaneURL: "https://api.test", adapterVersion: "vercel-workflow/1" as const };
beforeEach(() => request.mockReset());

it("records the engine identity before acknowledging a new submission", async () => {
  request.mockResolvedValueOnce({ claimed: true, claim_token: "exclusive" }).mockResolvedValueOnce({ runtime_run_id: "engine-1" });
  const submit = vi.fn(async () => ({ runtimeRunId: "engine-1" }));
  expect(await startWithReceipt(input, submit)).toEqual({ runtimeRunId: "engine-1" });
  expect(request.mock.calls[1]![2]).toMatchObject({ claim_token: "exclusive", runtime_run_id: "engine-1" });
  expect(submit).toHaveBeenCalledTimes(1);
});
it("recovers a lost response without resubmitting the engine run", async () => {
  request.mockResolvedValueOnce({ claimed: false, runtime_run_id: "engine-1" });
  const submit = vi.fn();
  expect(await startWithReceipt(input, submit)).toEqual({ runtimeRunId: "engine-1" });
  expect(submit).not.toHaveBeenCalled();
});
it("does not reclaim an ambiguous submission after the worker crashes", async () => {
  request.mockResolvedValueOnce({ claimed: true, claim_token: "exclusive" }).mockRejectedValueOnce(new Error("receipt response lost"));
  const submit = vi.fn(async () => ({ runtimeRunId: "engine-1" }));
  await expect(startWithReceipt(input, submit)).rejects.toThrow("receipt response lost");
  request.mockResolvedValueOnce({ claimed: false });
  await expect(startWithReceipt(input, submit)).rejects.toThrow("workflow_start_unconfirmed");
  expect(submit).toHaveBeenCalledTimes(1);
});
it("does not submit on a lost claim response or release a claim on engine failure", async () => {
  const submit = vi.fn(async () => { throw new Error("engine response lost"); });
  request.mockRejectedValueOnce(new Error("claim response lost"));
  await expect(startWithReceipt(input, submit)).rejects.toThrow("claim response lost");
  expect(submit).not.toHaveBeenCalled();
  request.mockResolvedValueOnce({ claimed: true, claim_token: "exclusive" });
  await expect(startWithReceipt(input, submit)).rejects.toThrow("engine response lost");
  expect(request).toHaveBeenCalledTimes(2);
});
