import { createAnthropic } from "@ai-sdk/anthropic";
import { createGoogleGenerativeAI } from "@ai-sdk/google";
import { createOpenAI } from "@ai-sdk/openai";
import { createOpenAICompatible } from "@ai-sdk/openai-compatible";
import type {
  EmbeddingModelV4,
  LanguageModelV4,
  LanguageModelV4CallOptions,
  SpeechModelV4,
  TranscriptionModelV3,
  TranscriptionModelV4,
} from "@ai-sdk/provider";
import { createGateway } from "ai";
import { z } from "zod";
import { chatgptLanguageModel } from "./chatgpt-provider.js";

/**
 * Models run on the device's own key for the model's provider (src/device-models.ts), and only
 * on that key.
 */
export const providerIds = [
  "openai",
  "anthropic",
  "google",
  "openrouter",
  "gateway",
  "nvidia",
  "ollama",
  "ollama-cloud",
  "openai-compatible",
  "chatgpt",
  "elevenlabs",
  "soniox",
] as const;
export type ProviderId = (typeof providerIds)[number];

/** Older names kept so existing model IDs keep working: `nim/` evals and `gemini/` judges. */
const providerAliases: Record<string, ProviderId> = { nim: "nvidia", gemini: "google" };

const credentialSchema = z.object({
  provider: z.enum(providerIds),
  apiKey: z.string().max(8192),
  baseURL: z.string().max(2048).optional(),
});

/**
 * The device's key for a model and its reasoning. Its base URL may name a server on the
 * person's own computer or network, such as a local Ollama.
 */
export const modelRouteSchema = z.object({
  provider: z.literal("device"),
  reasoning: z.string().max(16).optional(),
  credential: credentialSchema,
});
export type ModelRoute = z.infer<typeof modelRouteSchema>;
export type ModelCredential = z.infer<typeof credentialSchema>;

/**
 * A model that can't run as asked. The message never carries a key or a provider's response,
 * so it is safe for workflow history, logs and the person.
 */
export class ModelSetupError extends Error {
  constructor(
    readonly code: "model_key_needed" | "model_unsupported" | "model_base_url_invalid",
    readonly detail: string,
  ) {
    super(`${code}: ${detail}`);
    this.name = "ModelSetupError";
  }
}

const keyNeeded = () => new ModelSetupError("model_key_needed", "Add a model key in Settings → Models.");

/** `<provider>/<model as that provider names it>`, with the old aliases resolved. */
export function parseModelId(modelId: string): { provider: ProviderId; model: string } {
  const slash = modelId.indexOf("/");
  const prefix = slash > 0 ? modelId.slice(0, slash) : "";
  const model = slash > 0 ? modelId.slice(slash + 1) : "";
  const provider = providerAliases[prefix] ?? (providerIds as readonly string[]).find((id) => id === prefix);
  if (!provider || !model || /\s/.test(model) || modelId.length > 200)
    throw new ModelSetupError("model_unsupported", "Use a model ID of the form provider/model.");
  return { provider: provider as ProviderId, model };
}

/** Providers that run without a key (a local server). */
const keyOptional = new Set<ProviderId>(["ollama", "openai-compatible"]);

function deviceBaseURL(value: string | undefined): string | undefined {
  if (!value?.trim()) return undefined;
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new ModelSetupError("model_base_url_invalid", "The model's base URL isn't a valid URL.");
  }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
    throw new ModelSetupError("model_base_url_invalid", "The model's base URL must be an http(s) address.");
  return url.toString().replace(/\/$/, "");
}

interface Target {
  provider: ProviderId;
  model: string;
  credential: ModelCredential;
}

/** Which provider, model name and key one call uses. */
export function resolveTarget(modelId: string, route: ModelRoute): Target {
  const { provider, model } = parseModelId(modelId);
  const credential = route.credential;
  // A key is used only for its own provider.
  if (credential.provider !== provider) throw keyNeeded();
  if (!credential.apiKey.trim() && !keyOptional.has(provider)) throw keyNeeded();
  const baseURL = deviceBaseURL(credential.baseURL);
  if (provider === "openai-compatible" && !baseURL)
    throw new ModelSetupError("model_base_url_invalid", "An OpenAI-compatible model needs a base URL.");
  return { provider, model, credential: { provider, apiKey: credential.apiKey.trim(), baseURL } };
}

/**
 * Replaces any key in an error, in place, so a provider that echoes it back can't carry it into
 * workflow history or logs. The error keeps its class and status, which retries and
 * classification read.
 */
export function scrubCredential<T>(error: T, secrets: string[]): T {
  const live = secrets.map((secret) => secret.trim()).filter((secret) => secret.length >= 8);
  if (!live.length) return error;
  const clean = (text: string) => live.reduce((value, secret) => value.split(secret).join("[redacted]"), text);
  const seen = new Set<object>();
  const visit = (value: unknown, depth: number): unknown => {
    if (typeof value === "string") return clean(value);
    if (!value || typeof value !== "object" || depth > 6 || seen.has(value)) return value;
    seen.add(value);
    if (value instanceof Uint8Array || value instanceof ArrayBuffer) return value;
    const keys = value instanceof Error ? ["message", "stack", "cause", ...Object.keys(value)] : Object.keys(value);
    for (const key of new Set(keys)) {
      try {
        const record = value as Record<string, unknown>;
        const next = visit(record[key], depth + 1);
        if (next !== record[key]) record[key] = next;
      } catch {
        // A read-only field stays as it was.
      }
    }
    return value;
  };
  return visit(error, 0) as T;
}

/** A model whose failures, thrown or streamed, never carry its key. */
function scrubbing(model: LanguageModelV4, secret: string): LanguageModelV4 {
  const secrets = [secret];
  return {
    specificationVersion: "v4",
    provider: model.provider,
    modelId: model.modelId,
    get supportedUrls() {
      return model.supportedUrls;
    },
    async doGenerate(options) {
      try {
        return await model.doGenerate(options);
      } catch (error) {
        throw scrubCredential(error, secrets);
      }
    },
    async doStream(options) {
      try {
        const result = await model.doStream(options);
        const stream = result.stream.pipeThrough(
          new TransformStream({
            transform(part, sink) {
              sink.enqueue(part.type === "error" ? { ...part, error: scrubCredential(part.error, secrets) } : part);
            },
          }),
        );
        return { ...result, stream };
      } catch (error) {
        throw scrubCredential(error, secrets);
      }
    },
  };
}

/** fetch for direct provider calls that logs why a request never reached the provider: the
 * SDK reports only "Cannot connect to API", without the network error under it. */
const loggedFetch: typeof fetch = async (input, init) => {
  try {
    return await fetch(input, init);
  } catch (error) {
    const cause = (error as { cause?: { code?: string; message?: string } }).cause;
    console.error("[model-fetch] request failed", String((error as Error).name), String((error as Error).message), cause?.code ?? "", cause?.message ?? "");
    throw error;
  }
};

/** The Gateway names `claude-haiku-5.5`; Anthropic's own API names `claude-haiku-5-5`. */
function anthropicModelName(model: string): string {
  return model.replaceAll(".", "-");
}

type Prompt = LanguageModelV4CallOptions["prompt"];

/**
 * Chat completions carry tool results as text only, so a screenshot in a tool result would
 * reach the model as base64 text. The screenshots move to a user message right after the
 * results, where they are images, and the results say so.
 */
export function imagesOutOfToolResults(prompt: Prompt): Prompt {
  const moved: Prompt = [];
  for (const message of prompt) {
    if (message.role !== "tool") {
      moved.push(message);
      continue;
    }
    const images: Array<Extract<Extract<Prompt[number], { role: "user" }>["content"][number], { type: "file" }>> = [];
    const content = message.content.map((part) => {
      if (part.type !== "tool-result" || part.output.type !== "content") return part;
      const shots = part.output.value.filter((item) => item.type === "file" && item.mediaType.startsWith("image"));
      if (!shots.length) return part;
      for (const shot of shots) if (shot.type === "file") images.push({ type: "file", data: shot.data, mediaType: shot.mediaType });
      const text = part.output.value.flatMap((item) => (item.type === "text" ? [item.text] : [])).join("\n");
      return { ...part, output: { type: "text" as const, value: `${text}\n(${shots.length} screenshot${shots.length === 1 ? "" : "s"} in the next message)` } };
    });
    moved.push({ ...message, content });
    if (images.length) moved.push({ role: "user", content: [{ type: "text", text: "Screenshots from the tool results above:" }, ...images] });
  }
  return moved;
}

/** Chat-completions endpoints: where each one lives by default. */
const compatibleBase: Partial<Record<ProviderId, string>> = {
  openrouter: "https://openrouter.ai/api/v1",
  nvidia: "https://integrate.api.nvidia.com/v1",
  ollama: "http://127.0.0.1:11434/v1",
  "ollama-cloud": "https://ollama.com/v1",
};

function compatible({ provider, credential }: Target) {
  return createOpenAICompatible({
    name: provider,
    apiKey: credential.apiKey || undefined,
    baseURL: credential.baseURL || compatibleBase[provider] || "",
    fetch: loggedFetch,
  });
}

function openAI({ credential }: Target) {
  return createOpenAI({ apiKey: credential.apiKey, baseURL: credential.baseURL, fetch: loggedFetch });
}

function gateway({ credential }: Target) {
  return createGateway({ apiKey: credential.apiKey, baseURL: credential.baseURL });
}

function google({ credential }: Target) {
  return createGoogleGenerativeAI({ apiKey: credential.apiKey, baseURL: credential.baseURL, fetch: loggedFetch });
}

const unsupported = (kind: string) => new ModelSetupError("model_unsupported", `This provider has no ${kind} models.`);

/** Gateway routing options mean nothing to any other provider. */
function withoutGatewayOptions(options: LanguageModelV4CallOptions): LanguageModelV4CallOptions {
  const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
  return { ...options, providerOptions };
}

/** Gateway model lists name models as the Gateway does, without a `gateway/` prefix. */
function gatewayOptions(options: LanguageModelV4CallOptions): LanguageModelV4CallOptions {
  const routing = options.providerOptions?.gateway as { models?: unknown } | undefined;
  if (!Array.isArray(routing?.models)) return options;
  const models = routing.models.map((model) => (typeof model === "string" ? model.replace(/^gateway\//, "") : model));
  return { ...options, providerOptions: { ...options.providerOptions, gateway: { ...routing, models } } };
}

function languageModel(target: Target, options: LanguageModelV4CallOptions): { model: LanguageModelV4; options: LanguageModelV4CallOptions } {
  const { provider, model } = target;
  switch (provider) {
    case "gateway":
      return { model: gateway(target)(model), options: gatewayOptions(options) };
    case "openai":
      return {
        model: openAI(target).responses(model),
        options: {
          ...withoutGatewayOptions(options),
          // Responses normalizes an omitted strict flag, which can turn optional
          // capability arguments into required fields. Preserve the admitted schema
          // unless the caller explicitly requested strict function calling.
          tools: options.tools?.map((tool) => (tool.type === "function" && tool.strict == null ? { ...tool, strict: false } : tool)),
        },
      };
    case "anthropic":
      return {
        model: createAnthropic({ apiKey: target.credential.apiKey, baseURL: target.credential.baseURL, fetch: loggedFetch })(anthropicModelName(model)),
        options: withoutGatewayOptions(options),
      };
    case "google":
      return { model: google(target)(model), options: withoutGatewayOptions(options) };
    case "openrouter": {
      const stripped = withoutGatewayOptions(options);
      return { model: compatible(target).chatModel(model), options: { ...stripped, prompt: imagesOutOfToolResults(stripped.prompt) } };
    }
    case "chatgpt":
      return chatgptLanguageModel(target.credential.apiKey, model, withoutGatewayOptions(options));
    case "nvidia":
    case "ollama":
    case "ollama-cloud":
    case "openai-compatible": {
      const stripped = withoutGatewayOptions(options);
      // These servers switch thinking their own way and may refuse reasoning_effort.
      return { model: compatible(target).chatModel(model), options: { ...stripped, prompt: imagesOutOfToolResults(stripped.prompt), reasoning: undefined } };
    }
    case "elevenlabs":
    case "soniox":
      throw unsupported("language");
  }
}

/** One language-model call on the route's key. */
export function resolveModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  route: ModelRoute,
): { model: LanguageModelV4; options: LanguageModelV4CallOptions } {
  const target = resolveTarget(modelId, route);
  const resolved = languageModel(target, options);
  return { model: scrubbing(resolved.model, target.credential.apiKey), options: resolved.options };
}

/** Applies the route's reasoning, then resolves the model on the route's key. */
export async function resolveConfiguredModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  route: ModelRoute,
): Promise<{ model: LanguageModelV4; options: LanguageModelV4CallOptions }> {
  return resolveModel(
    modelId,
    { ...options, reasoning: (route.reasoning || options.reasoning) as LanguageModelV4CallOptions["reasoning"] },
    route,
  );
}

/**
 * A run's model on the device's key, read in the Node model step and never returned from it:
 * the key exists only for this call, so it never reaches workflow history.
 */
export async function resolveRuntimeModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
): Promise<{ model: LanguageModelV4; options: LanguageModelV4CallOptions }> {
  const { deviceRoute } = await import("./device-models.js");
  return resolveConfiguredModel(modelId, options, deviceRoute(modelId));
}

export async function resolveEmbeddingModel(
  modelId: string,
  route: ModelRoute,
): Promise<EmbeddingModelV4> {
  const target = resolveTarget(modelId, route);
  switch (target.provider) {
    case "openai":
      return openAI(target).embedding(target.model);
    case "google":
      return google(target).embedding(target.model);
    case "gateway":
      return gateway(target).embeddingModel(target.model);
    case "openrouter":
    case "nvidia":
    case "ollama":
    case "ollama-cloud":
    case "openai-compatible":
      return compatible(target).embeddingModel(target.model);
    default:
      throw unsupported("embedding");
  }
}

export async function resolveSpeechModel(
  modelId: string,
  route: ModelRoute,
): Promise<SpeechModelV4> {
  const target = resolveTarget(modelId, route);
  switch (target.provider) {
    case "elevenlabs": {
      const { createElevenLabs } = await import("@ai-sdk/elevenlabs");
      return createElevenLabs({ apiKey: target.credential.apiKey }).speech(target.model);
    }
    case "openai":
      return openAI(target).speech(target.model);
    case "gateway":
      return gateway(target).speechModel(target.model);
    default:
      throw unsupported("speech");
  }
}

export async function resolveTranscriptionModel(
  modelId: string,
  route: ModelRoute,
): Promise<TranscriptionModelV4 | TranscriptionModelV3> {
  const target = resolveTarget(modelId, route);
  switch (target.provider) {
    case "soniox": {
      const { createSoniox } = await import("@soniox/vercel-ai-sdk-provider");
      return createSoniox({ apiKey: target.credential.apiKey, apiBaseUrl: target.credential.baseURL }).transcription(target.model);
    }
    case "openai":
      return openAI(target).transcription(target.model);
    case "gateway":
      return gateway(target).transcriptionModel(target.model);
    default:
      throw unsupported("transcription");
  }
}
