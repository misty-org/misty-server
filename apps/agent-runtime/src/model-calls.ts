import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { embedMany, generateText, jsonSchema, Output, transcribe, type ModelMessage } from "ai";
import { z } from "zod";
import {
  resolveConfiguredModel,
  resolveEmbeddingModel,
  resolveTranscriptionModel,
  type ModelRoute,
} from "./model-provider.js";

// One-shot model calls the Go API makes outside agent runs: Library analysis,
// embeddings, transcription, the screen planner and short completions. Go
// chooses the model and meters each call; the runtime only talks to models,
// always through the AI SDK and always on Misty's own keys.

const modelId = z.string().min(3).max(200).regex(/^[^\s/]+\/\S+$/);

const route = z.object({
  provider: z.literal("instance"),
  reasoning: z.string().max(16).optional(),
});

const reasoning = z.enum(["provider-default", "none", "minimal", "low", "medium", "high", "xhigh"]);

const base64 = z.string().max(24 << 20).regex(/^[A-Za-z0-9+/]*={0,2}$/);

const textPart = z.object({ type: z.literal("text"), text: z.string().max(512 << 10) });
const imagePart = z.object({
  type: z.literal("image"),
  mediaType: z.enum(["image/jpeg", "image/png", "image/webp", "image/gif"]),
  data: base64,
});

const textRequest = z.object({
  route,
  model: modelId,
  system: z.string().max(64 << 10).optional(),
  messages: z
    .array(
      z.object({
        role: z.enum(["user", "assistant"]),
        content: z.array(z.union([textPart, imagePart])).min(1).max(64),
      }),
    )
    .min(1)
    .max(64),
  maxOutputTokens: z.number().int().min(1).max(32_000),
  reasoning: reasoning.optional(),
  schema: z.object({ name: z.string().regex(/^[A-Za-z0-9_-]{1,64}$/), schema: z.record(z.string(), z.unknown()) }).optional(),
});

const embedRequest = z.object({
  route,
  model: modelId,
  values: z.array(z.string().max(256 << 10)).min(1).max(100),
  dimensions: z.number().int().min(1).max(4096).optional(),
  images: z.array(z.object({ mediaType: z.enum(["image/jpeg", "image/png", "image/webp"]), data: base64 }).nullable()).optional(),
});

const transcribeRequest = z.object({
  route,
  model: modelId,
  mediaType: z.string().max(100).regex(/^audio\/[A-Za-z0-9.+-]+(?:;.*)?$/),
  audio: base64,
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

/** Applies the reasoning Go admitted to each language-model call. */
class RoutedModel implements LanguageModelV4 {
  readonly specificationVersion = "v4" as const;
  readonly provider = "misty.routed";
  readonly supportedUrls = {};

  constructor(
    readonly modelId: string,
    private readonly route: ModelRoute,
  ) {}

  async doGenerate(options: LanguageModelV4CallOptions) {
    const resolved = await resolveConfiguredModel(this.modelId, options, this.route);
    return resolved.model.doGenerate(resolved.options);
  }

  async doStream(options: LanguageModelV4CallOptions) {
    const resolved = await resolveConfiguredModel(this.modelId, options, this.route);
    return resolved.model.doStream(resolved.options);
  }
}

function usageOf(usage: {
  inputTokens?: number;
  outputTokens?: number;
  inputTokenDetails?: { cacheReadTokens?: number };
  outputTokenDetails?: { reasoningTokens?: number };
}) {
  return {
    inputTokens: usage.inputTokens ?? 0,
    cachedInputTokens: usage.inputTokenDetails?.cacheReadTokens ?? 0,
    outputTokens: usage.outputTokens ?? 0,
    reasoningTokens: usage.outputTokenDetails?.reasoningTokens ?? 0,
  };
}

export async function generateTextCall(body: unknown, signal?: AbortSignal) {
  const request = parse(textRequest, body);
  const messages: ModelMessage[] = request.messages.map((message) =>
    message.role === "assistant"
      ? {
          role: "assistant",
          content: message.content.flatMap((part) => (part.type === "text" ? [{ type: "text" as const, text: part.text }] : [])),
        }
      : {
          role: "user",
          content: message.content.map((part) =>
            part.type === "text"
              ? { type: "text" as const, text: part.text }
              : { type: "file" as const, data: Buffer.from(part.data, "base64"), mediaType: part.mediaType },
          ),
        },
  );
  const common = {
    model: new RoutedModel(request.model, request.route),
    system: request.system,
    messages,
    maxOutputTokens: request.maxOutputTokens,
    reasoning: request.reasoning,
    maxRetries: 1,
    abortSignal: signal,
  };
  if (request.schema) {
    const result = await generateText({
      ...common,
      output: Output.object({ schema: jsonSchema(request.schema.schema), name: request.schema.name }),
    });
    return { text: result.text, object: result.output, usage: usageOf(result.usage), finishReason: result.finishReason };
  }
  const result = await generateText(common);
  return { text: result.text, usage: usageOf(result.usage), finishReason: result.finishReason };
}

export async function embedCall(body: unknown, signal?: AbortSignal) {
  const request = parse(embedRequest, body);
  if (request.images && request.images.length !== request.values.length)
    throw new ModelCallError(400, "invalid_model_call", "Each image must match a value");
  const images = request.images?.some(Boolean) ? request.images : undefined;
  const dimensions = request.dimensions;
  const result = await embedMany({
    model: await resolveEmbeddingModel(request.model),
    values: request.values,
    maxRetries: 1,
    abortSignal: signal,
    providerOptions: {
      ...(dimensions ? { openai: { dimensions }, "openai-compatible": { dimensions } } : {}),
      google: {
        ...(dimensions ? { outputDimensionality: dimensions } : {}),
        ...(images
          ? { content: images.map((image) => (image ? [{ inlineData: { mimeType: image.mediaType, data: image.data } }] : null)) }
          : {}),
      },
    },
  });
  return { embeddings: result.embeddings, usage: { inputTokens: result.usage.tokens ?? 0 } };
}

export async function transcribeCall(body: unknown, signal?: AbortSignal) {
  const request = parse(transcribeRequest, body);
  const result = await transcribe({
    model: await resolveTranscriptionModel(request.model),
    audio: Buffer.from(request.audio, "base64"),
    maxRetries: 1,
    abortSignal: signal,
  });
  return {
    text: result.text,
    language: result.language ?? "",
    durationInSeconds: result.durationInSeconds ?? null,
    segments: result.segments.map((segment) => ({
      start: segment.startSecond,
      end: segment.endSecond,
      text: segment.text,
    })),
  };
}

/** Provider failures keep only a status: response bodies may echo credentials. */
export function modelCallFailure(error: unknown): { status: number; body: Record<string, unknown> } {
  if (error instanceof ModelCallError) return { status: error.status, body: { code: error.code, message: error.message } };
  const status = (error as { statusCode?: unknown })?.statusCode;
  const upstream = typeof status === "number" && status >= 400 && status < 600 ? status : 0;
  return {
    status: 502,
    body: { code: "model_call_failed", message: "The model provider could not complete the request.", upstream_status: upstream },
  };
}
