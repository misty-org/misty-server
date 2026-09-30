import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { WORKFLOW_DESERIALIZE, WORKFLOW_SERIALIZE } from "@workflow/serde";

/** Persist only the model ID. Resolve operator credentials inside the model step. */
export class InstanceModel implements LanguageModelV4 {
  readonly specificationVersion = "v4" as const;
  readonly provider = "misty.instance";
  readonly supportedUrls = {};

  constructor(readonly modelId: string) {}

  static [WORKFLOW_SERIALIZE](model: InstanceModel) {
    return { modelId: model.modelId };
  }

  static [WORKFLOW_DESERIALIZE](value: { modelId: string }) {
    return new InstanceModel(value.modelId);
  }

  async doGenerate(options: LanguageModelV4CallOptions) {
    const { resolveInstanceModel } = await import("./model-provider.js");
    const resolved = resolveInstanceModel(this.modelId, options);
    return resolved.model.doGenerate(resolved.options);
  }

  async doStream(options: LanguageModelV4CallOptions) {
    const { resolveInstanceModel } = await import("./model-provider.js");
    const resolved = resolveInstanceModel(this.modelId, options);
    return resolved.model.doStream(resolved.options);
  }
}
