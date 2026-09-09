import { z } from "zod";
import {
  MistyRoutineExecutionSchema, MistyCapabilityValueSchema, MistyCapabilityOutcomeSchema,
  type MistyRoutineValue, type MistyRoutineReference, type MistyRoutineCondition,
  type MistyRoutineStep, type MistyRoutineAgentBinding,
} from "@misty/contracts";

export type RoutineStepState = "completed" | "partial" | "skipped" | "failed" | "uncertain" | "not_run";
export interface RoutineReport {
  state: "completed" | "partial" | "failed" | "uncertain";
  steps: Array<{ stepId: string; state: RoutineStepState; callId?: string }>;
  code?: string;
}
export interface RoutineExecutionPorts {
  executeCapability(callId: string, toolName: string, input: unknown): Promise<unknown>;
  /** Refresh admission authority and the persisted active-time budget. */
  beforeStep(stepId: string, index: number, activeSeconds: number): Promise<void>;
  /** Audit metadata only. Actual results stay in durable, protected tool storage. */
  checkpoint(stepId: string, state: RoutineStepState, callId?: string): Promise<void>;
  /** The pinned worker must provide the adapters required by this definition. */
  agent?(step: Extract<MistyRoutineStep, { kind: "agent" }>, prompt: string, binding: MistyRoutineAgentBinding): Promise<unknown>;
  wait?(stepId: string, until: string): Promise<unknown>;
}
const missing = Symbol("missing routine value");
type Values = { trigger: unknown; steps: Map<string, unknown> };
function reference(ref: MistyRoutineReference, values: Values): unknown | typeof missing {
  let item: unknown = ref.source.kind === "trigger" ? values.trigger : values.steps.has(ref.source.stepId) ? values.steps.get(ref.source.stepId) : missing;
  for (const key of ref.path) {
    if (item === missing || !item || typeof item !== "object" || !Object.hasOwn(item, key)) return missing;
    if (Array.isArray(item) ? typeof key !== "number" : typeof key !== "string") return missing;
    item = (item as Record<string | number, unknown>)[key];
  }
  return item;
}
export function routineValue(expression: MistyRoutineValue, values: Values): unknown {
  switch (expression.kind) {
    case "literal": return expression.value;
    case "reference": {
      const result = reference(expression, values);
      if (result === missing) throw new Error("routine_reference_missing");
      return result;
    }
    case "array": return expression.items.map(item => routineValue(item, values));
    case "object": return Object.fromEntries(Object.entries(expression.fields).map(([key, item]) => [key, routineValue(item, values)]));
  }
}
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`).join(",")}}`;
  return JSON.stringify(value);
}
export function routineCondition(condition: MistyRoutineCondition, values: Values): boolean {
  switch (condition.kind) {
    case "exists": return reference(condition.reference, values) !== missing;
    case "equals": return canonical(routineValue(condition.left, values)) === canonical(routineValue(condition.right, values));
    case "all": return condition.conditions.every(item => routineCondition(item, values));
    case "any": return condition.conditions.some(item => routineCondition(item, values));
    case "not": return !routineCondition(condition.condition, values);
  }
}
function confirmedOutput(raw: unknown): { state: "completed" | "partial" | "failed" | "uncertain"; value?: unknown } {
  if (raw === undefined) return { state: "failed" };
  if (raw && typeof raw === "object") {
    const result = raw as Record<string, unknown>;
    if (result.status === "uncertain") return { state: "uncertain" };
    if (result.denied === true || result.unavailable === true || ["failure", "approval_required", "device_required", "user_intervention_required"].includes(String(result.status))) return { state: "failed" };
    if (result.status === "success") {
      const parsed = MistyCapabilityOutcomeSchema.safeParse(result);
      if (!parsed.success || parsed.data.status !== "success") return { state: "failed" };
      return { state: parsed.data.partial ? "partial" : "completed", value: parsed.data.result };
    }
    if (result.partial === true || result.truncated === true) return { state: "partial", value: raw };
  }
  return { state: "completed", value: raw };
}

/** Called inside the pinned workflow. Replay follows its recorded durable tool
 * results with the same Go-issued call IDs; it never reruns the original prompt,
 * chooses another provider, retries an uncertain effect, or audits raw results. */
export async function executeRoutine(
  input: unknown, runId: string, advertised: ReadonlySet<string>, ports: RoutineExecutionPorts,
): Promise<RoutineReport> {
  const execution = MistyRoutineExecutionSchema.parse(input);
  if (execution.runId !== runId) throw new Error("routine_run_mismatch");
  const bindings = new Map(execution.bindings.map(binding => [binding.stepId, binding]));
  const agentBindings = new Map(execution.agentBindings.map(binding => [binding.stepId, binding]));
  // Fail before any effect if this pinned worker lacks any requested adapter.
  for (const step of execution.definition.steps) {
    if (step.kind === "agent" && !ports.agent || step.kind === "wait" && !ports.wait) throw new Error("routine_adapter_unavailable");
    if (step.kind === "capability" && !advertised.has(bindings.get(step.id)!.toolName)) throw new Error("routine_capability_unavailable");
    if (step.kind === "agent" && agentBindings.get(step.id)!.tools.some(tool => !advertised.has(tool.toolName))) throw new Error("routine_capability_unavailable");
  }
  const values: Values = { trigger: execution.trigger, steps: new Map() };
  const report: RoutineReport = { state: "completed", steps: [] };
  let retainedBytes = 0;
  const stop = (state: RoutineReport["state"], code: string) => {
    report.state = state; report.code = code;
    for (const step of execution.definition.steps.slice(report.steps.length)) report.steps.push({ stepId: step.id, state: "not_run" });
    return report;
  };
  for (const [index, step] of execution.definition.steps.entries()) {
    await ports.beforeStep(step.id, index, execution.definition.budget.activeSeconds);
    let resolved: unknown;
    try {
      if (step.when && !routineCondition(step.when, values)) {
        await ports.checkpoint(step.id, "skipped"); report.steps.push({ stepId: step.id, state: "skipped" }); continue;
      }
      resolved = routineValue(step.kind === "capability" ? step.input : step.kind === "agent" ? step.prompt : step.until, values);
      MistyCapabilityValueSchema.parse(resolved);
      if ((step.kind === "agent" || step.kind === "wait") && typeof resolved !== "string") throw new Error("routine_value_type");
      if (step.kind === "wait") {
        z.iso.datetime({ offset: true }).parse(resolved);
        if (Date.parse(resolved as string) - Date.now() > 24 * 60 * 60 * 1000) throw new Error("routine_wait_expiry_limit");
      }
    } catch {
      await ports.checkpoint(step.id, "failed"); report.steps.push({ stepId: step.id, state: "failed" });
      return stop("failed", "routine_input_unresolved");
    }
    const binding = bindings.get(step.id);
    let outcome: ReturnType<typeof confirmedOutput>;
    try {
      const raw = step.kind === "capability"
        ? await ports.executeCapability(binding!.callId, binding!.toolName, resolved)
        : step.kind === "agent" ? await ports.agent!(step, resolved as string, agentBindings.get(step.id)!)
        : await ports.wait!(step.id, resolved as string);
      outcome = confirmedOutput(raw);
      if (outcome.state === "completed" || outcome.state === "partial") {
        const value = MistyCapabilityValueSchema.parse(outcome.value);
        retainedBytes += new TextEncoder().encode(JSON.stringify(value)).length;
        if (retainedBytes > 2 * 1024 * 1024) throw new Error("routine_replay_data_limit");
        values.steps.set(step.id, value);
      }
    } catch {
      // The transport may fail after a write. Preserve the call identity for
      // journal reconciliation and do not proceed or auto-retry this action.
      outcome = { state: "uncertain" };
    }
    const callId = binding?.callId ?? agentBindings.get(step.id)?.callNamespace;
    await ports.checkpoint(step.id, outcome.state, callId);
    report.steps.push({ stepId: step.id, state: outcome.state, ...(callId ? { callId } : {}) });
    if (outcome.state === "uncertain") return stop("uncertain", "routine_effect_uncertain");
    if (outcome.state === "failed") return stop("failed", "routine_step_unconfirmed");
    if (outcome.state === "partial") {
      report.state = "partial";
      if (step.kind !== "capability" || !step.allowPartial) return stop("partial", "routine_partial_result");
    }
  }
  return report;
}
