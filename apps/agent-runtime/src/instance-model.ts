import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { WORKFLOW_DESERIALIZE, WORKFLOW_SERIALIZE } from "@workflow/serde";

import type { RuntimeIdentity } from "./control-plane.js";

/** Persist public routing identity only. Resolve keys inside each model step. */
export class InstanceModel implements LanguageModelV4 {
  readonly specificationVersion = "v4" as const;
  readonly provider = "misty.instance";
  readonly supportedUrls = {};

  constructor(
    readonly modelId: string,
    readonly identity?: RuntimeIdentity,
    readonly role: "agent" | "vision" = "agent",
  ) {}

  static [WORKFLOW_SERIALIZE](model: InstanceModel) {
    return { modelId: model.modelId, identity: model.identity, role: model.role };
  }

  static [WORKFLOW_DESERIALIZE](value: {
    modelId: string;
    identity?: RuntimeIdentity;
    role?: "agent" | "vision";
  }) {
    return new InstanceModel(value.modelId, value.identity, value.role);
  }

  async doGenerate(options: LanguageModelV4CallOptions) {
    "use step";
    const { resolveRuntimeModel } = await import("./model-provider.js");
    const resolved = await resolveRuntimeModel(this.modelId, options, this.identity, this.role);
    return resolved.model.doGenerate(resolved.options);
  }

  async doStream(options: LanguageModelV4CallOptions) {
    "use step";
    const { resolveRuntimeModel } = await import("./model-provider.js");
    const resolved = await resolveRuntimeModel(this.modelId, options, this.identity, this.role);
    return resolved.model.doStream(resolved.options);
  }
}
