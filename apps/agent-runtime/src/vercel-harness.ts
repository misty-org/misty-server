import { startWithReceipt } from "./start-receipt.js";
import { getRun, start } from "workflow/api";
import { runSpaceTaskAgent } from "../workflows/space-task-agent.js";
import { agentToolApprovalHook } from "./approval.js";
import { agentDeviceHook } from "./device.js";
import { MISTY_HARNESS_VERSION, type MistyHarness } from "./harness.js";

/** The beta has one engine. Unavailable pinned engines are never substituted. */
export const vercelHarness: MistyHarness = {
  version: MISTY_HARNESS_VERSION,
  async start(input) {
    if (input.adapterVersion !== MISTY_HARNESS_VERSION) throw new Error("harness_version_unavailable");
    return startWithReceipt(input, async () => {
      const run = await start(runSpaceTaskAgent, [input]);
      return { runtimeRunId: run.runId };
    });
  },
  async status(id) {
    const run = getRun(id);
    return (await run.exists) ? await run.status : "missing";
  },
  async cancel(id) { await getRun(id).cancel(); },
  async resume(input) {
    if (input.kind === "approval") {
      await agentToolApprovalHook.resume(input.token, { approved: input.approved, approval_id: input.approvalId });
    } else {
      await agentDeviceHook.resume(input.token, { available: input.available });
    }
  },
};
