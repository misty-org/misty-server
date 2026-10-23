import { createAnthropic } from "@ai-sdk/anthropic";
import { createOpenAI } from "@ai-sdk/openai";
import { createOpenAICompatible } from "@ai-sdk/openai-compatible";
import type {
  EmbeddingModelV4,
  LanguageModelV4,
  LanguageModelV4CallOptions,
  SpeechModelV4,
  TranscriptionModelV4,
} from "@ai-sdk/provider";
import { createGateway } from "ai";
import type { RuntimeIdentity } from "./control-plane.js";

type Environment = Record<string, string | undefined>;

/**
 * Every model runs on Misty's own keys. OpenAI and Anthropic models go straight to
 * their maker when the instance has that key; everything else goes through the AI Gateway.
 */
export function instanceModelConfig(env: Environment = process.env) {
  const model = env.MISTY_AGENT_MODEL?.trim() || "";
  if (model && (!/^[^\s/]+\/\S+$/.test(model) || model.length > 200)) {
    throw new Error("MISTY_AGENT_MODEL must be a provider/model ID");
  }
  return { provider: "gateway", model };
}

/** The route Go admitted for a call: always Misty's own, with its reasoning. */
export interface ModelRoute {
  provider: "instance";
  reasoning?: string;
}

function openAIKey(env: Environment) {
  return env.OPENAI_API_KEY?.trim() || "";
}

/** An `openai/…` model with an instance OpenAI key skips the Gateway. */
function directOpenAI(modelId: string, env: Environment) {
  return modelId.startsWith("openai/") && openAIKey(env) !== "";
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

function anthropicKey(env: Environment) {
  return env.ANTHROPIC_API_KEY?.trim() || env.CLAUDE_API_KEY?.trim() || "";
}

/** An `anthropic/…` model with an instance Anthropic key skips the Gateway. */
function directAnthropic(modelId: string, env: Environment) {
  return modelId.startsWith("anthropic/") && anthropicKey(env) !== "";
}

/** The Gateway names `claude-haiku-5.5`; Anthropic's own API names `claude-haiku-5-5`. */
function anthropicModelName(modelId: string): string {
  return modelId.slice("anthropic/".length).replaceAll(".", "-");
}

function nimKey(env: Environment) {
  return env.NVIDIA_API_KEY?.trim() || "";
}

/**
 * A `nim/…` model runs on NVIDIA's hosted endpoints (build.nvidia.com), free with rate limits,
 * so evals can run without paid credit (docs/agents/evals/free-model.md). Development only:
 * production never admits a model the person didn't get from the catalog.
 */
function directNim(modelId: string, env: Environment) {
  return modelId.startsWith("nim/") && nimKey(env) !== "";
}

function nim(modelId: string, env: Environment) {
  return createOpenAICompatible({
    name: "nim",
    apiKey: nimKey(env),
    baseURL: env.NVIDIA_BASE_URL?.trim() || "https://integrate.api.nvidia.com/v1",
    fetch: loggedFetch,
  }).chatModel(modelId.slice("nim/".length));
}

function geminiKey(env: Environment) {
  return env.GEMINI_API_KEY?.trim() || "";
}

/**
 * A `gemini/…` model runs on Google's Gemini API through its OpenAI-compatible endpoint. It is
 * the Odysseys benchmark's own judge family (gemini-3.1-flash-lite). Development only, like `nim/`.
 */
function directGemini(modelId: string, env: Environment) {
  return modelId.startsWith("gemini/") && geminiKey(env) !== "";
}

function gemini(modelId: string, env: Environment) {
  return createOpenAICompatible({
    name: "gemini",
    apiKey: geminiKey(env),
    baseURL: env.GEMINI_BASE_URL?.trim() || "https://generativelanguage.googleapis.com/v1beta/openai",
    fetch: loggedFetch,
  }).chatModel(modelId.slice("gemini/".length));
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

function openAI(env: Environment) {
  return createOpenAI({ apiKey: openAIKey(env) });
}

function instanceGateway(env: Environment = process.env) {
  return createGateway({
    apiKey: env.AI_GATEWAY_API_KEY?.trim() || env.VERCEL_OIDC_TOKEN?.trim() || undefined,
    baseURL: env.AI_GATEWAY_BASE_URL?.trim() || undefined,
  });
}

export function resolveInstanceModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  env: Environment = process.env,
): { model: LanguageModelV4; options: LanguageModelV4CallOptions } {
  if (directAnthropic(modelId, env)) {
    // Gateway routing options mean nothing to Anthropic itself.
    const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
    return {
      model: createAnthropic({ apiKey: anthropicKey(env), fetch: loggedFetch })(anthropicModelName(modelId)),
      options: { ...options, providerOptions },
    };
  }
  if (directNim(modelId, env)) {
    const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
    // NVIDIA's models switch thinking their own way and may refuse reasoning_effort.
    return { model: nim(modelId, env), options: { ...options, prompt: imagesOutOfToolResults(options.prompt), reasoning: undefined, providerOptions } };
  }
  if (directGemini(modelId, env)) {
    const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
    return { model: gemini(modelId, env), options: { ...options, prompt: imagesOutOfToolResults(options.prompt), reasoning: undefined, providerOptions } };
  }
  if (!directOpenAI(modelId, env)) return { model: instanceGateway(env)(modelId), options };
  // Gateway routing options mean nothing to OpenAI itself.
  const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
  return {
    model: openAI(env).responses(modelId.slice("openai/".length)),
    options: {
      ...options,
      // Responses normalizes an omitted strict flag, which can turn optional
      // capability arguments into required fields. Preserve the admitted schema
      // unless the caller explicitly requested strict function calling.
      tools: options.tools?.map((tool) =>
        tool.type === "function" && tool.strict == null ? { ...tool, strict: false } : tool,
      ),
      providerOptions,
    },
  };
}

/** Resolve the run's reasoning in the Node model step, never in the workflow bundle. */
export async function resolveRuntimeModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  identity?: RuntimeIdentity,
  role: "agent" | "vision" = "agent",
): Promise<{ model: LanguageModelV4; options: LanguageModelV4CallOptions }> {
  if (!identity) return resolveInstanceModel(modelId, options);
  const { controlPlaneRequest } = await import("./control-plane.js");
  const route = await controlPlaneRequest<ModelRoute>(
    identity,
    "model-provider",
    { role, model: modelId },
    `${role}:provider`,
  );
  return resolveConfiguredModel(modelId, options, route);
}

export async function resolveConfiguredModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  route: ModelRoute,
): Promise<{ model: LanguageModelV4; options: LanguageModelV4CallOptions }> {
  return resolveInstanceModel(modelId, {
    ...options,
    reasoning: (route.reasoning || options.reasoning) as LanguageModelV4CallOptions["reasoning"],
  });
}

export async function resolveEmbeddingModel(
  modelId: string,
  env: Environment = process.env,
): Promise<EmbeddingModelV4> {
  if (directOpenAI(modelId, env)) return openAI(env).embedding(modelId.slice("openai/".length));
  return instanceGateway(env).embeddingModel(modelId);
}

export async function resolveSpeechModel(
  modelId: string,
  env: Environment = process.env,
): Promise<SpeechModelV4> {
  // ElevenLabs speaks the voice pipeline on Misty's own key.
  if (modelId.startsWith("elevenlabs/")) {
    const apiKey = env.ELEVENLABS_API_KEY?.trim();
    if (!apiKey) throw new Error("ELEVENLABS_API_KEY is not configured");
    const { createElevenLabs } = await import("@ai-sdk/elevenlabs");
    return createElevenLabs({ apiKey }).speech(modelId.slice("elevenlabs/".length));
  }
  if (directOpenAI(modelId, env)) return openAI(env).speech(modelId.slice("openai/".length));
  return instanceGateway(env).speechModel(modelId);
}

export async function resolveTranscriptionModel(
  modelId: string,
  env: Environment = process.env,
): Promise<TranscriptionModelV4> {
  if (directOpenAI(modelId, env))
    return openAI(env).transcription(modelId.slice("openai/".length));
  return instanceGateway(env).transcriptionModel(modelId);
}
