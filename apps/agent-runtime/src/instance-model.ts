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
    return watchStream((signal) => resolved.model.doStream({ ...resolved.options, abortSignal: signal }), resolved.options.abortSignal);
  }
}

/** How long a model stream may go without a part before it counts as stalled. */
export const STREAM_STALL_MS = 120_000;

/**
 * A provider stream that stops sending would otherwise hold the step for the run's whole time
 * budget. Waiting longer than `STREAM_STALL_MS` for the response or for its next part aborts
 * the request and fails the step with `model_stream_stalled`, so the workflow retries it.
 */
export async function watchStream<T extends { stream: ReadableStream<P> }, P>(
  start: (signal: AbortSignal) => PromiseLike<T>,
  outer?: AbortSignal,
  stallMs = STREAM_STALL_MS,
): Promise<T> {
  const controller = new AbortController();
  const signal = outer ? AbortSignal.any([outer, controller.signal]) : controller.signal;
  const stalled = new Error("model_stream_stalled");
  let timer: ReturnType<typeof setTimeout> | undefined;
  const arm = () => {
    clearTimeout(timer);
    timer = setTimeout(() => controller.abort(stalled), stallMs);
  };
  arm();
  let result: T;
  try {
    result = await start(signal);
  } catch (error) {
    clearTimeout(timer);
    throw controller.signal.aborted ? stalled : error;
  }
  arm();
  const reader = result.stream.getReader();
  const stream = new ReadableStream<P>({
    async pull(sink) {
      try {
        const next = await Promise.race([
          reader.read(),
          new Promise<never>((_, reject) => controller.signal.addEventListener("abort", () => reject(stalled), { once: true })),
        ]);
        if (next.done) {
          clearTimeout(timer);
          sink.close();
          return;
        }
        arm();
        sink.enqueue(next.value);
      } catch (error) {
        clearTimeout(timer);
        void reader.cancel().catch(() => undefined);
        sink.error(controller.signal.aborted ? stalled : error);
      }
    },
    cancel(reason) {
      clearTimeout(timer);
      return reader.cancel(reason);
    },
  });
  return { ...result, stream };
}
