import { expect, it, vi } from "vitest";
import { executeRoutine, type RoutineExecutionPorts } from "../src/routine-coordinator.js";
const pin = { capability: "habits.list", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000001", targetRevision: 1 };
const literal = (value: unknown) => ({ kind: "literal", value });
const fixture = () => ({
  routineId: "10000000-0000-4000-8000-000000000002", version: 1, runId: "run_routine", trigger: { date: "today" },
  definition: { protocol: 1, name: "Daily habits", trigger: { kind: "manual" }, budget: { modelTurns: 20, activeSeconds: 1800 }, steps: [
    { id: "read", label: "Read", kind: "capability", action: pin, input: literal({}), allowPartial: false },
    { id: "save", label: "Save", kind: "capability", action: pin, input: { kind: "object", fields: { items: { kind: "reference", source: { kind: "step", stepId: "read" }, path: ["items"] } } }, allowPartial: false },
  ] },
  bindings: ["read", "save"].map((stepId, i) => ({ stepId, callId: `10000000-0000-4000-8000-00000000000${i + 3}`, toolName: `sdk.${stepId}` })),
});
const advertised = new Set(["sdk.read", "sdk.save"]);
const ports = (): RoutineExecutionPorts => ({ executeCapability: vi.fn(async () => ({ items: ["walk"] })), beforeStep: vi.fn(async () => {}), checkpoint: vi.fn(async () => {}) });
it("uses admitted identities and prior results, with no private content in checkpoints", async () => {
  const p = ports(); const input = fixture();
  const report = await executeRoutine(input, input.runId, advertised, p);
  expect(report.state).toBe("completed");
  expect(p.executeCapability).toHaveBeenNthCalledWith(2, input.bindings[1]!.callId, "sdk.save", { items: ["walk"] });
  expect(JSON.stringify(vi.mocked(p.checkpoint).mock.calls)).not.toContain("walk");
  expect(p.beforeStep).toHaveBeenCalledTimes(2);
});
it("replays with the same calls and saved results instead of creating new effects", async () => {
  const input = fixture(); const p = ports(); const receipts = new Map<string, unknown>(); let writes = 0;
  p.executeCapability = async (id, name) => {
    if (receipts.has(id)) return receipts.get(id);
    if (name === "sdk.save") writes++;
    const result = { items: ["walk"] }; receipts.set(id, result); return result;
  };
  vi.mocked(p.checkpoint).mockImplementationOnce(async () => { throw new Error("lost checkpoint response"); });
  await expect(executeRoutine(input, input.runId, advertised, p)).rejects.toThrow();
  await executeRoutine(input, input.runId, advertised, p);
  await executeRoutine(input, input.runId, advertised, p);
  expect(writes).toBe(1); expect(receipts.size).toBe(2);
});
it.each([undefined, { denied: true }, { status: "failure", code: "denied" }, { status: "success", partial: false }])("does not run dependents after unconfirmed output %j", async result => {
  const p = ports(); vi.mocked(p.executeCapability).mockResolvedValueOnce(result);
  const report = await executeRoutine(fixture(), "run_routine", advertised, p);
  expect(report.state).toBe("failed"); expect(report.steps[1]!.state).toBe("not_run"); expect(p.executeCapability).toHaveBeenCalledTimes(1);
});
it("preserves an uncertain call and does not retry a possible send", async () => {
  const p = ports(); vi.mocked(p.executeCapability).mockResolvedValueOnce({ status: "uncertain" });
  const input = fixture(); const report = await executeRoutine(input, input.runId, advertised, p);
  expect(report.state).toBe("uncertain"); expect(report.steps[0]!.callId).toBe(input.bindings[0]!.callId); expect(p.executeCapability).toHaveBeenCalledTimes(1);
});
it("requires explicit permission to continue from partial data", async () => {
  const p = ports(); const input = fixture();
  vi.mocked(p.executeCapability).mockResolvedValueOnce({ status: "success", result: { items: ["walk"] }, partial: true, evidence: [] });
  expect((await executeRoutine(input, input.runId, advertised, p)).steps[1]!.state).toBe("not_run");
  input.definition.steps[0]!.allowPartial = true;
  vi.mocked(p.executeCapability).mockResolvedValueOnce({ status: "success", result: { items: ["walk"] }, partial: true, evidence: [] });
  const report = await executeRoutine(input, input.runId, advertised, p);
  expect(report.state).toBe("partial"); expect(report.steps[1]!.state).toBe("completed");
});
it("rejects a different run or unavailable adapter before the first effect", async () => {
  const input = fixture(); const p = ports();
  await expect(executeRoutine(input, "other-run", advertised, p)).rejects.toThrow("routine_run_mismatch");
  await expect(executeRoutine(input, input.runId, new Set(["sdk.read"]), p)).rejects.toThrow("routine_capability_unavailable");
  const extended = { ...input, definition: { ...input.definition, steps: [...input.definition.steps, { kind: "wait", id: "later", label: "Wait", until: literal("2099-01-01T00:00:00Z") }] } };
  await expect(executeRoutine(extended, input.runId, advertised, p)).rejects.toThrow("routine_adapter_unavailable");
  expect(p.executeCapability).not.toHaveBeenCalled();
});
it("stops when execution authority disappears between steps", async () => {
  const p = ports(); vi.mocked(p.beforeStep).mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("revoked"));
  await expect(executeRoutine(fixture(), "run_routine", advertised, p)).rejects.toThrow("revoked");
  expect(p.executeCapability).toHaveBeenCalledTimes(1);
});
