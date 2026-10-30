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
import type { RuntimeIdentity } from "./control-plane.js";

type Environment = Record<string, string | undefined>;

/**
 * Models run on the key the route names. An `account` route carries the person's own key for
 * the model's provider and uses only that. An `instance` route uses the runtime's environment
 * keys, set by whoever runs the server (single-user self-hosting and evals).
 */
export const providerIds = [
  "openai",
  "anthropic",
  "google",
  "openrouter",
  "gateway",
  "nvidia",
  "ollama",
  "openai-compatible",
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

/** The route Go sends with a call: whose key the model runs on, and its reasoning. */
export const modelRouteSchema = z.discriminatedUnion("provider", [
  z.object({ provider: z.literal("instance"), reasoning: z.string().max(16).optional() }),
  z.object({ provider: z.literal("account"), reasoning: z.string().max(16).optional(), credential: credentialSchema }),
]);
export type ModelRoute = z.infer<typeof modelRouteSchema>;
export type ModelCredential = z.infer<typeof credentialSchema>;

const instanceRoute: ModelRoute = { provider: "instance" };

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

/** The operator's default agent model, checked at startup. */
export function instanceModelConfig(env: Environment = process.env) {
  const model = env.MISTY_AGENT_MODEL?.trim() || "";
  if (model) {
    try {
      parseModelId(model);
    } catch {
      throw new Error("MISTY_AGENT_MODEL must be a provider/model ID with a known provider");
    }
  }
  return { provider: "instance" as const, model };
}

const first = (...values: Array<string | undefined>) => values.map((value) => value?.trim() ?? "").find(Boolean) ?? "";

/** The runtime's own key for a provider, from its environment. */
function instanceCredential(provider: ProviderId, env: Environment): ModelCredential | undefined {
  const keyed = (apiKey: string, baseURL?: string) => (apiKey ? { provider, apiKey, baseURL: baseURL || undefined } : undefined);
  switch (provider) {
    case "openai":
      return keyed(first(env.OPENAI_API_KEY), first(env.OPENAI_BASE_URL));
    case "anthropic":
      return keyed(first(env.ANTHROPIC_API_KEY, env.CLAUDE_API_KEY), first(env.ANTHROPIC_BASE_URL));
    case "google":
      return keyed(first(env.GEMINI_API_KEY, env.GOOGLE_GENERATIVE_AI_API_KEY));
    case "openrouter":
      return keyed(first(env.OPENROUTER_API_KEY), first(env.OPENROUTER_BASE_URL));
    case "gateway":
      return keyed(first(env.AI_GATEWAY_API_KEY, env.VERCEL_OIDC_TOKEN), first(env.AI_GATEWAY_BASE_URL));
    case "nvidia":
      return keyed(first(env.NVIDIA_API_KEY), first(env.NVIDIA_BASE_URL));
    case "ollama":
      // A local Ollama needs no key.
      return { provider, apiKey: first(env.OLLAMA_API_KEY), baseURL: first(env.OLLAMA_BASE_URL) || undefined };
    case "openai-compatible": {
      const baseURL = first(env.OPENAI_COMPATIBLE_BASE_URL);
      return baseURL ? { provider, apiKey: first(env.OPENAI_COMPATIBLE_API_KEY), baseURL } : undefined;
    }
    case "elevenlabs":
      return keyed(first(env.ELEVENLABS_API_KEY));
    case "soniox":
      return keyed(first(env.SONIOX_API_KEY));
  }
}

/** Providers the AI Gateway also serves under the same `<maker>/<model>` names. */
const gatewayServes = new Set<ProviderId>(["openai", "anthropic", "google"]);

/** Providers that run without a key (a local server). */
const keyOptional = new Set<ProviderId>(["ollama", "openai-compatible"]);

/** Hosts an account's base URL may not name: this runtime, its private network, cloud metadata. */
function blockedHost(host: string): boolean {
  const name = host.toLowerCase().replace(/^\[|\]$/g, "");
  if (name === "host.docker.internal") return false;
  if (!name.includes(".") && !name.includes(":")) return true; // localhost and compose service names
  if (name.endsWith(".localhost") || name.endsWith(".internal") || name.endsWith(".local")) return true;
  if (/^(127\.|0\.|169\.254\.)/.test(name) || name === "::1" || name === "::" || name.startsWith("fe80:")) return true;
  return false;
}

function accountBaseURL(value: string | undefined): string | undefined {
  if (!value?.trim()) return undefined;
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new ModelSetupError("model_base_url_invalid", "The model's base URL isn't a valid URL.");
  }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || blockedHost(url.hostname))
    throw new ModelSetupError("model_base_url_invalid", "The model's base URL must be a public http(s) address.");
  return url.toString().replace(/\/$/, "");
}

interface Target {
  provider: ProviderId;
  model: string;
  credential: ModelCredential;
}

/** Which provider, model name and key one call uses. */
export function resolveTarget(modelId: string, route: ModelRoute = instanceRoute, env: Environment = process.env): Target {
  const { provider, model } = parseModelId(modelId);
  if (route.provider === "account") {
    const credential = route.credential;
    // An account's key is used only for its own provider, never swapped for the instance's.
    if (credential.provider !== provider) throw keyNeeded();
    if (!credential.apiKey.trim() && !keyOptional.has(provider)) throw keyNeeded();
    const baseURL = accountBaseURL(credential.baseURL);
    if (provider === "openai-compatible" && !baseURL)
      throw new ModelSetupError("model_base_url_invalid", "An OpenAI-compatible model needs a base URL.");
    return { provider, model, credential: { provider, apiKey: credential.apiKey.trim(), baseURL } };
  }
  const own = instanceCredential(provider, env);
  if (own) return { provider, model, credential: own };
  // The operator's Gateway key also serves the makers it carries.
  const gateway = gatewayServes.has(provider) ? instanceCredential("gateway", env) : undefined;
  if (gateway) return { provider: "gateway", model: `${provider}/${model}`, credential: gateway };
  throw keyNeeded();
}

/** The keys a failure must never repeat: the route's own, or the instance's. */
export function routeSecrets(route: unknown, env: Environment = process.env): string[] {
  const parsed = modelRouteSchema.safeParse(route);
  if (parsed.success && parsed.data.provider === "account") return [parsed.data.credential.apiKey];
  return providerIds.flatMap((id) => instanceCredential(id, env)?.apiKey || []);
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
  ollama: "http://host.docker.internal:11434/v1",
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
    case "nvidia":
    case "ollama":
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
  route: ModelRoute = instanceRoute,
  env: Environment = process.env,
): { model: LanguageModelV4; options: LanguageModelV4CallOptions } {
  const target = resolveTarget(modelId, route, env);
  const resolved = languageModel(target, options);
  return { model: scrubbing(resolved.model, target.credential.apiKey), options: resolved.options };
}

/** One call on the instance's own environment keys. */
export function resolveInstanceModel(modelId: string, options: LanguageModelV4CallOptions, env: Environment = process.env) {
  return resolveModel(modelId, options, instanceRoute, env);
}

/** Applies the route's reasoning, then resolves the model on the route's key. */
export async function resolveConfiguredModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  route: ModelRoute,
  env: Environment = process.env,
): Promise<{ model: LanguageModelV4; options: LanguageModelV4CallOptions }> {
  return resolveModel(
    modelId,
    { ...options, reasoning: (route.reasoning || options.reasoning) as LanguageModelV4CallOptions["reasoning"] },
    route,
    env,
  );
}

/**
 * A run's route, read in the Node model step and never returned from it: the key exists only
 * for this call, so it never reaches workflow history.
 */
export async function resolveRuntimeModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  identity?: RuntimeIdentity,
  role: "agent" | "vision" = "agent",
): Promise<{ model: LanguageModelV4; options: LanguageModelV4CallOptions }> {
  if (!identity) return resolveInstanceModel(modelId, options);
  const { controlPlaneRequest } = await import("./control-plane.js");
  const response = await controlPlaneRequest<unknown>(identity, "model-provider", { role, model: modelId }, `${role}:provider`);
  const route = modelRouteSchema.safeParse(response);
  // The response may hold a key, so neither it nor the parse error is repeated.
  if (!route.success) throw new Error("invalid_model_route: Misty sent a model route the runtime can't read.");
  return resolveConfiguredModel(modelId, options, route.data);
}

export async function resolveEmbeddingModel(
  modelId: string,
  route: ModelRoute = instanceRoute,
  env: Environment = process.env,
): Promise<EmbeddingModelV4> {
  const target = resolveTarget(modelId, route, env);
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
    case "openai-compatible":
      return compatible(target).embeddingModel(target.model);
    default:
      throw unsupported("embedding");
  }
}

export async function resolveSpeechModel(
  modelId: string,
  route: ModelRoute = instanceRoute,
  env: Environment = process.env,
): Promise<SpeechModelV4> {
  const target = resolveTarget(modelId, route, env);
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
  route: ModelRoute = instanceRoute,
  env: Environment = process.env,
): Promise<TranscriptionModelV4 | TranscriptionModelV3> {
  const target = resolveTarget(modelId, route, env);
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
