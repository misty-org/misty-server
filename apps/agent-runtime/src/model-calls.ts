import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { embedMany, generateSpeech } from "ai";
import { z } from "zod";
import {
  modelRouteSchema as route,
  ModelSetupError,
  resolveConfiguredModel,
  resolveEmbeddingModel,
  resolveSpeechModel,
  type ModelRoute,
} from "./model-provider.js";

// The model-call building blocks the desktop's calls (src/device-calls.ts) and its voice share.
// Each runs on the device's key for the model's provider, always through the AI SDK.

const modelId = z.string().min(3).max(200).regex(/^[^\s/]+\/\S+$/);

const base64 = z.string().max(24 << 20).regex(/^[A-Za-z0-9+/]*={0,2}$/);

const embedRequest = z.object({
  route,
  model: modelId,
  values: z.array(z.string().max(256 << 10)).min(1).max(100),
  dimensions: z.number().int().min(1).max(4096).optional(),
  images: z.array(z.object({ mediaType: z.enum(["image/jpeg", "image/png", "image/webp"]), data: base64 }).nullable()).optional(),
});

// Spoken replies for the voice pipeline: raw 24 kHz 16-bit mono PCM, the
// format the voice session already streams to the app.
const speechRequest = z.object({
  route,
  model: modelId,
  text: z.string().min(1).max(8000),
  // An OpenAI voice name or an ElevenLabs voice ID.
  voice: z.string().regex(/^[A-Za-z0-9_-]{1,64}$/).optional(),
  // A reply spoken sentence by sentence: its neighbours keep the delivery
  // continuous across the joins (ElevenLabs only).
  previousText: z.string().max(4000).optional(),
  nextText: z.string().max(4000).optional(),
});

export class ModelCallError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

function parse<T extends z.ZodTypeAny>(schema: T, body: unknown): z.infer<T> {
  const result = schema.safeParse(body);
  if (!result.success) throw new ModelCallError(400, "invalid_model_call", "Invalid model call");
  return result.data;
}

/** Runs each language-model call on the route's key, with its reasoning. The route stays in
 * memory for this one call and is never serialized. */
export class RoutedModel implements LanguageModelV4 {
  readonly specificationVersion = "v4" as const;
  readonly provider = "misty.routed";
  readonly supportedUrls = {};

  constructor(
    readonly modelId: string,
    route: ModelRoute,
  ) {
    // Not an own enumerable field, so logging or serializing the model can't reach the key.
    Object.defineProperty(this, "route", { value: route, enumerable: false });
  }

  private declare readonly route: ModelRoute;

  async doGenerate(options: LanguageModelV4CallOptions) {
    const resolved = await resolveConfiguredModel(this.modelId, options, this.route);
    return resolved.model.doGenerate(resolved.options);
  }

  async doStream(options: LanguageModelV4CallOptions) {
    const resolved = await resolveConfiguredModel(this.modelId, options, this.route);
    return resolved.model.doStream(resolved.options);
  }
}

export async function embedCall(body: unknown, signal?: AbortSignal) {
  const request = parse(embedRequest, body);
  if (request.images && request.images.length !== request.values.length)
    throw new ModelCallError(400, "invalid_model_call", "Each image must match a value");
  const images = request.images?.some(Boolean) ? request.images : undefined;
  const dimensions = request.dimensions;
  const result = await embedMany({
    model: await resolveEmbeddingModel(request.model, request.route),
    values: request.values,
    maxRetries: 1,
    abortSignal: signal,
    providerOptions: {
      // Each chat-completions provider reads its options under its own name.
      ...(dimensions
        ? Object.fromEntries(["openai", "openai-compatible", "openrouter", "nvidia", "ollama"].map((name) => [name, { dimensions }]))
        : {}),
      google: {
        ...(dimensions ? { outputDimensionality: dimensions } : {}),
        ...(images
          ? { content: images.map((image) => (image ? [{ inlineData: { mimeType: image.mediaType, data: image.data } }] : null)) }
          : {}),
      },
    },
  });
  return { embeddings: result.embeddings };
}

export async function speechCall(body: unknown, signal?: AbortSignal) {
  const request = parse(speechRequest, body);
  const elevenLabs = request.model.startsWith("elevenlabs/");
  const result = await generateSpeech({
    model: await resolveSpeechModel(request.model, request.route),
    text: request.text,
    // ElevenLabs picks its default voice when none is configured.
    voice: request.voice ?? (elevenLabs ? undefined : "alloy"),
    // OpenAI's "pcm" is 24 kHz; ElevenLabs needs the rate spelled out.
    outputFormat: elevenLabs ? "pcm_24000" : "pcm",
    ...(elevenLabs && (request.previousText || request.nextText)
      ? { providerOptions: { elevenlabs: { previousText: request.previousText, nextText: request.nextText } } }
      : {}),
    maxRetries: 1,
    abortSignal: signal,
  });
  return { audio: Buffer.from(result.audio.uint8Array).toString("base64"), mediaType: result.audio.mediaType };
}

/** Provider failures keep only a status: response bodies may echo credentials. */
export function modelCallFailure(error: unknown): { status: number; body: Record<string, unknown> } {
  if (error instanceof ModelCallError) return { status: error.status, body: { code: error.code, message: error.message } };
  if (error instanceof ModelSetupError)
    return { status: error.code === "model_key_needed" ? 409 : 400, body: { code: error.code, message: error.detail } };
  const status = (error as { statusCode?: unknown })?.statusCode;
  const upstream = typeof status === "number" && status >= 400 && status < 600 ? status : 0;
  return {
    status: 502,
    body: { code: "model_call_failed", message: "The model provider could not complete the request.", upstream_status: upstream },
  };
}
