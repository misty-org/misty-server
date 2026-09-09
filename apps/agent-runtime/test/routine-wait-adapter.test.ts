import { expect, it, vi } from "vitest";
import { routineWaitAdapter } from "../src/routine-wait-adapter.js";
const until = "2026-09-08T09:00:00Z";
const receipt = (state = "waiting") => ({ stepId: "pause", waitId: "10000000-0000-4000-8000-000000000001", state, until, expiresAt: "2026-09-09T08:00:00Z", remainingMs: state === "waiting" ? 1000 : 0 });
const controls = () => ({ open: vi.fn(async () => receipt()), resume: vi.fn(async () => receipt("completed")), sleep: vi.fn(async (_milliseconds: number) => {}) });
it("sleeps durably then requires Go's exact completion receipt", async () => {
  const control = controls();
  expect(await routineWaitAdapter(control)("pause", until)).toMatchObject({ status: "success", result: { resumed: true } });
  expect(control.sleep).toHaveBeenCalledWith(1000);
  expect(control.resume).toHaveBeenCalledWith("pause", receipt().waitId);
});
it("replays a committed wait without another sleep or wake-up", async () => {
  const control = controls(); control.open.mockResolvedValue(receipt("completed"));
  await routineWaitAdapter(control)("pause", until);
  expect(control.sleep).not.toHaveBeenCalled(); expect(control.resume).not.toHaveBeenCalled();
});
it("keeps the original wait identity through an early wake", async () => {
  const control = controls(); control.resume.mockResolvedValueOnce(receipt()).mockResolvedValueOnce(receipt("completed"));
  await routineWaitAdapter(control)("pause", until);
  expect(control.sleep).toHaveBeenCalledTimes(2);
  expect(control.resume.mock.calls).toEqual([["pause", receipt().waitId], ["pause", receipt().waitId]]);
});
it("does not turn missing, expired or replaced wait receipts into success", async () => {
  const control = controls(); control.resume.mockResolvedValue(receipt("expired"));
  expect(await routineWaitAdapter(control)("pause", until)).toMatchObject({ status: "failure" });
  control.resume.mockResolvedValue({ ...receipt("completed"), waitId: "10000000-0000-4000-8000-000000000002" });
  await expect(routineWaitAdapter(control)("pause", until)).rejects.toThrow("routine_wait_resume_mismatch");
  control.resume.mockResolvedValue({} as any);
  await expect(routineWaitAdapter(control)("pause", until)).rejects.toThrow();
});
