import { controlPlaneRequest, type RuntimeIdentity } from "./control-plane.js";
import type { HarnessCheckpoint, HarnessCompletion } from "./harness.js";
import type { ExecutionBudget } from "./model-budget.js";
import { rethrowStepError } from "./runtime-errors.js";
import type { SpaceTaskContext } from "./types.js";

// Durable run lifecycle calls. Each is one idempotent workflow step.

export async function activateRuntime(identity: RuntimeIdentity): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(identity, "activate", { runtime_kind: "vercel-workflow" }, `${identity.mistyRunId}:activate`);
  } catch (error) {
    rethrowStepError(error);
  }
}

export async function fetchContext(identity: RuntimeIdentity): Promise<SpaceTaskContext> {
  "use step";
  try {
    return await controlPlaneRequest<SpaceTaskContext>(identity, "context", {}, `${identity.mistyRunId}:context`);
  } catch (error) {
    rethrowStepError(error);
  }
}

export async function takeSteering(identity: RuntimeIdentity, boundary: string, close: boolean) {
  "use step";
  try {
    return await controlPlaneRequest<{ messages: Array<{ sequence: number; text: string }>; closed: boolean }>(
      identity, "steering", { boundary, close }, `${identity.mistyRunId}:steering:${boundary}`,
    );
  } catch (error) {
    rethrowStepError(error);
  }
}

export async function fetchExecutionBudget(identity: RuntimeIdentity, turn: number): Promise<ExecutionBudget> {
  "use step";
  try {
    return await controlPlaneRequest<ExecutionBudget>(identity, "budget", { begin: true }, `${identity.mistyRunId}:budget:${turn}`);
  } catch (error) {
    rethrowStepError(error);
  }
}

export async function checkpoint(identity: RuntimeIdentity, event: HarnessCheckpoint): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(identity, "events", { attempt: 1, output: {}, ...event }, `${identity.mistyRunId}:event:${event.node_id}:${event.state}`);
  } catch (error) {
    rethrowStepError(error);
  }
}

export async function complete(identity: RuntimeIdentity, result: HarnessCompletion): Promise<void> {
  "use step";
  try {
    await controlPlaneRequest(identity, "complete", { ...result }, `${identity.mistyRunId}:complete`);
  } catch (error) {
    rethrowStepError(error);
  }
}
