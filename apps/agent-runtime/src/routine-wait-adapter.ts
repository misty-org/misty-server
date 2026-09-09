import { z } from "zod";

const receiptSchema = z.strictObject({
  stepId: z.string().min(1).max(100), waitId: z.string().uuid(),
  state: z.enum(["waiting", "completed", "expired", "cancelled"]),
  until: z.iso.datetime({ offset: true }), expiresAt: z.iso.datetime({ offset: true }),
  remainingMs: z.number().int().min(0).max(24 * 60 * 60 * 1000),
});
export interface RoutineWaitControl {
  open(stepId: string, until: string): Promise<unknown>;
  resume(stepId: string, waitId: string): Promise<unknown>;
  /** The pinned engine supplies durable sleep; no process-local timer is used. */
  sleep(milliseconds: number): Promise<void>;
}

/** Sleeping alone cannot complete a routine step. Go checks its exact wait ID,
 * persisted deadline, current permissions and cancellation before acknowledgement. */
export function routineWaitAdapter(control: RoutineWaitControl) {
  return async (stepId: string, until: string): Promise<unknown> => {
    let receipt = receiptSchema.parse(await control.open(stepId, until));
    const requested = Date.parse(until), admitted = Date.parse(receipt.until);
    // Go rounds submillisecond deadlines up to the engine's millisecond clock.
    if (receipt.stepId !== stepId || !Number.isFinite(requested) || admitted < requested || admitted > requested + 1)
      throw new Error("routine_wait_admission_mismatch");
    const pinned = receipt;
    for (let wakes = 0; receipt.state === "waiting" && wakes < 3; wakes++) {
      // Relative time from Go also tolerates clock skew between worker regions.
      await control.sleep(Math.max(1, receipt.remainingMs));
      receipt = receiptSchema.parse(await control.resume(stepId, pinned.waitId));
      if (receipt.stepId !== stepId || receipt.waitId !== pinned.waitId || receipt.until !== pinned.until || receipt.expiresAt !== pinned.expiresAt)
        throw new Error("routine_wait_resume_mismatch");
    }
    if (receipt.state !== "completed") return { status: "failure", code: "routine_wait_unconfirmed", message: "The routine's timed wait did not resume successfully.", retryable: false };
    return { status: "success", result: { resumed: true }, partial: false, evidence: [] };
  };
}
