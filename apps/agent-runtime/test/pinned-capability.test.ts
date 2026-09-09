import { describe, expect, it, vi } from "vitest";
import { executePinnedCapability } from "../src/pinned-capability.js";

describe("host-admitted SDK execution", () => {
  const execution = {
    name: "sdk.exact-provider-target",
    call_id: "10000000-0000-4000-8000-000000000001",
    input: { habit: "walk" },
  };
  it("preserves the host effect identity and input through transport recovery", async () => {
    const execute = vi.fn().mockRejectedValueOnce(new Error("lost response")).mockResolvedValue({ status: "success" });
    const catalog = new Set([execution.name]);
    await expect(executePinnedCapability(execution, catalog, execute)).rejects.toThrow("lost response");
    await executePinnedCapability(execution, catalog, execute);
    expect(execute.mock.calls).toEqual([
      [execution.call_id, execution.name, execution.input],
      [execution.call_id, execution.name, execution.input],
    ]);
  });
  it("stops when the pinned provider disappears instead of choosing a replacement", async () => {
    const execute = vi.fn();
    await expect(executePinnedCapability(execution, new Set(["sdk.replacement"]), execute)).rejects.toThrow("pinned_capability_unavailable");
    expect(execute).not.toHaveBeenCalled();
  });
  it("rejects malformed identities before dispatch", async () => {
    const execute = vi.fn();
    for (const call_id of ["", "------------------------------------", "new-call"]) {
      await expect(executePinnedCapability({ ...execution, call_id }, new Set([execution.name]), execute)).rejects.toThrow("pinned_capability_unavailable");
    }
    expect(execute).not.toHaveBeenCalled();
  });
});
