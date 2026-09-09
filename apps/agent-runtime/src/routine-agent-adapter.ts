import { MistyCapabilityOutcomeSchema, type MistyRoutineAgentBinding, type MistyRoutineStep } from "@misty/contracts";
import type { MCPRemoteTool } from "./types.js";
import { runRoutineAgent, type RoutineAgentPorts, type RoutineAgentResult } from "./routine-agent.js";

type AgentStep = Extract<MistyRoutineStep, { kind: "agent" }>;
export interface RoutineAgentControl extends RoutineAgentPorts {
  /** Go checks the current step and derives the prompt from protected receipts. */
  open(stepId: string): Promise<
    | { state: "replayed"; outcome: unknown }
    | { state: "running"; protocol: 1; stepId: string; callNamespace: string; prompt: string; modelId: string; maxTurns: number; reasoning?: "low" | "medium" | "high" }
  >;
  /** Go validates output schema and effect evidence, then commits the checkpoint. */
  finish(stepId: string, result: RoutineAgentResult): Promise<unknown>;
}

/** The coordinator can only consume a result acknowledged by Go. A model's
 * structured answer alone is never a completed saved-routine checkpoint. */
export function routineAgentAdapter(catalog: MCPRemoteTool[], control: RoutineAgentControl) {
  return async (step: AgentStep, resolvedPrompt: string, binding: MistyRoutineAgentBinding): Promise<unknown> => {
    const opened = await control.open(step.id);
    if (opened.state === "replayed") return MistyCapabilityOutcomeSchema.parse(opened.outcome);
    if (opened.state !== "running" || opened.protocol !== 1 || opened.stepId !== step.id || opened.callNamespace !== binding.callNamespace || opened.prompt !== resolvedPrompt || opened.maxTurns !== step.maxTurns)
      throw new Error("routine_agent_admission_mismatch");
    const result = await runRoutineAgent({ step, binding, catalog, prompt: opened.prompt, modelId: opened.modelId, reasoning: opened.reasoning }, control);
    return MistyCapabilityOutcomeSchema.parse(await control.finish(step.id, result));
  };
}
