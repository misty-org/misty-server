import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { controlPlaneRequest, type RuntimeIdentity } from "./control-plane.js";
import type { HarnessCheckpoint } from "./harness.js";

// Step-only helpers. Loaded from inside the compaction step so their Node
// dependencies never reach the workflow bundle.

/** Calls the model through the run's own route; credentials resolve in this step. */
export class RunModel implements LanguageModelV4 {
  readonly specificationVersion = "v4" as const;
  readonly provider = "misty.compaction";
  readonly supportedUrls = {};
  constructor(
    readonly modelId: string,
    private readonly identity: RuntimeIdentity,
  ) {}
  async doGenerate(options: LanguageModelV4CallOptions) {
    const { resolveRuntimeModel } = await import("./model-provider.js");
    const resolved = await resolveRuntimeModel(this.modelId, options, this.identity, "agent");
    return resolved.model.doGenerate(resolved.options);
  }
  async doStream(options: LanguageModelV4CallOptions) {
    const { resolveRuntimeModel } = await import("./model-provider.js");
    const resolved = await resolveRuntimeModel(this.modelId, options, this.identity, "agent");
    return resolved.model.doStream(resolved.options);
  }
}

// Already inside a step: report progress directly rather than nesting steps.
export async function report(identity: RuntimeIdentity, event: HarnessCheckpoint) {
  await controlPlaneRequest(identity, "events", { attempt: 1, output: {}, ...event }, `${identity.mistyRunId}:event:${event.node_id}:${event.state}`);
}
