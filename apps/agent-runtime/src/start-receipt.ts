import { controlPlaneRequest } from "./control-plane.js";
import type { HarnessStart } from "./harness.js";

interface StartReceipt {
  claimed: boolean;
  claim_token?: string;
  runtime_run_id?: string;
}

/** No in-memory lock: all workers coordinate through the admitted Misty run. */
export async function startWithReceipt(
  input: HarnessStart,
  submit: () => Promise<{ runtimeRunId: string }>,
): Promise<{ runtimeRunId: string }> {
  const identity = { ...input, runtimeRunId: "" };
  const payload = { adapter_version: input.adapterVersion, callback_url: input.controlPlaneURL };
  const receipt = await controlPlaneRequest<StartReceipt>(identity, "start-receipt", payload, `${input.mistyRunId}:start-claim`);
  if (receipt.runtime_run_id) return { runtimeRunId: receipt.runtime_run_id };
  if (!receipt.claimed || !receipt.claim_token) throw new Error("workflow_start_unconfirmed: awaiting the original runtime identity");
  // An engine exception may follow a committed submission. Do not release the
  // claim: later delivery recovers the activation binding or reports uncertainty.
  const run = await submit();
  await controlPlaneRequest<StartReceipt>(identity, "start-receipt", {
    ...payload, claim_token: receipt.claim_token, runtime_run_id: run.runtimeRunId,
  }, `${input.mistyRunId}:start-record`);
  return run;
}
