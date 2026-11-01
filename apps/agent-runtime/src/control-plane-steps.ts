import { FatalError } from "workflow";
import { controlPlaneRequest, type RuntimeIdentity } from "./control-plane.js";
import { deviceModelId, deviceReasoning, deviceSearch } from "./device-models.js";
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
  // The run thinks on the device's models; the server learns which, to show them and size the
  // context window. The choice is read here, inside the step, so a replay of the run keeps the
  // models it started with. So is its web search: with a search key the device runs web.search
  // itself, and tells the server so the run's prompt describes it.
  const device = deviceModelId("agent");
  if (!device) throw new FatalError("model_key_needed: Add a model key in Settings → Models.");
  try {
    const choice = { model_id: device, vision_model_id: device, reasoning_effort: deviceReasoning() };
    const search = deviceSearch()?.provider;
    const context = await controlPlaneRequest<SpaceTaskContext>(identity, "context", { ...choice, device_tools: search ? ["web.search"] : [] }, `${identity.mistyRunId}:context`);
    return { ...context, ...choice, device_search: search } as SpaceTaskContext;
  } catch (error) {
    rethrowStepError(error);
  }
}

export async function takeSteering(identity: RuntimeIdentity, boundary: string, close: boolean) {
  "use step";
  try {
    return await controlPlaneRequest<{ messages: Array<{ sequence: number; text: string }>; closed: boolean; paused?: boolean }>(
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

export async function complete(
  identity: RuntimeIdentity,
  result: HarnessCompletion,
  titlePrompt?: string,
  summaryRequest?: { previous: string; turns: string },
): Promise<void> {
  "use step";
  // A new chat is named, and a long chat's aged-out turns become notes, on the device's fast
  // model. A run never fails for either.
  const done = result.status === "success";
  const calls = done ? await import("./device-calls.js") : undefined;
  const title = calls && titlePrompt ? await calls.titleFor(titlePrompt, result.text).catch(() => "") : "";
  const summary = calls && summaryRequest ? await calls.summaryFor(summaryRequest.previous, summaryRequest.turns).catch(() => "") : "";
  const extras = { ...(title ? { title } : {}), ...(summary ? { summary, model: deviceModelId("agent") } : {}) };
  try {
    await controlPlaneRequest(identity, "complete", { ...result, ...extras }, `${identity.mistyRunId}:complete`);
  } catch (error) {
    rethrowStepError(error);
  }
}
