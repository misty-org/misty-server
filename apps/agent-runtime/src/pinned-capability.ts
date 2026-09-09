/** Host-issued deterministic action; the model cannot substitute another tool. */
export interface PinnedCapabilityExecution {
  name: string;
  call_id: string;
  input: unknown;
}

export async function executePinnedCapability(
  execution: PinnedCapabilityExecution,
  advertised: ReadonlySet<string>,
  execute: (callId: string, name: string, input: unknown) => Promise<unknown>,
): Promise<unknown> {
  if (!execution || !advertised.has(execution.name) || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(execution.call_id)) {
    throw new Error("pinned_capability_unavailable: the exact admitted action is unavailable");
  }
  return execute(execution.call_id, execution.name, execution.input);
}
