import { FatalError, getWorkflowMetadata } from "workflow";
import { DocumentIds } from "../src/document-ids.js";
import type { RuntimeIdentity } from "../src/control-plane.js";
import { activateRuntime, checkpoint, complete, fetchContext } from "../src/control-plane-steps.js";
import { MISTY_HARNESS_VERSION, type HarnessExecution } from "../src/harness.js";
import { classifyRuntimeError } from "../src/runtime-errors.js";
import { runTeachTurn } from "../src/teach-turn.js";
import { executeTool } from "../src/tool-calls.js";
import { rejectedWithoutEffect } from "../src/tool-execution.js";
import type { SpaceTaskContext } from "../src/types.js";

export interface SpaceTaskWorkflowInput {
  adapterVersion?: typeof MISTY_HARNESS_VERSION;
  mistyRunId: string;
  controlPlaneURL: string;
}

/** What a started run works with, or a lesson step's finished result. */
export type RunStart =
  | { kind: "agent"; identity: RuntimeIdentity; context: SpaceTaskContext; execution: HarnessExecution; rejected: Set<string>; documents: DocumentIds }
  | { kind: "done"; result: { mistyRunId: string; text: string; steps: number; incomplete: boolean } };

/** Activates the run, reads its context, and answers a lesson step without the agent loop. */
export async function startRun(input: SpaceTaskWorkflowInput): Promise<RunStart> {
  if (input.adapterVersion && input.adapterVersion !== MISTY_HARNESS_VERSION) throw new FatalError("harness_version_unavailable");
  const identity: RuntimeIdentity = { mistyRunId: input.mistyRunId, runtimeRunId: getWorkflowMetadata().workflowRunId, controlPlaneURL: input.controlPlaneURL };
  const rejected = new Set<string>();
  const documents = new DocumentIds();
  const execution: HarnessExecution = {
    executeCapability: async (callId, name, value) => {
      try {
        const input = documents.repair(name, value);
        const output = await executeTool(identity, callId, name, input);
        documents.note(input, output);
        return output;
      } catch (error) {
        if (rejectedWithoutEffect(error)) rejected.add(callId);
        throw error;
      }
    },
    checkpoint: (event) => checkpoint(identity, event),
    complete: (result) => complete(identity, result),
  };
  await activateRuntime(identity);
  let context: SpaceTaskContext;
  try {
    context = await fetchContext(identity);
  } catch (error) {
    const failure = classifyRuntimeError(error);
    await execution.complete({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
    throw error;
  }
  if (context.teach) {
    // A lesson step: one streamed call, no tools (src/teach-turn.ts).
    try {
      const completion = await runTeachTurn(identity, context);
      await execution.complete(completion);
      return { kind: "done", result: { mistyRunId: input.mistyRunId, text: completion.text, steps: 1, incomplete: completion.status !== "success" } };
    } catch (error) {
      const failure = classifyRuntimeError(error);
      await execution.complete({ status: "failed", text: "", error_code: failure.code, error_message: failure.message });
      throw new FatalError(failure.message);
    }
  }
  return { kind: "agent", identity, context, execution, rejected, documents };
}
