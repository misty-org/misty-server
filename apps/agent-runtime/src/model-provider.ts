import { createOpenAI } from "@ai-sdk/openai";
import type {
  EmbeddingModelV4,
  LanguageModelV4,
  LanguageModelV4CallOptions,
  TranscriptionModelV4,
} from "@ai-sdk/provider";
import { createGateway } from "ai";
import type { RuntimeIdentity } from "./control-plane.js";

type Environment = Record<string, string | undefined>;

/**
 * Every model runs on Misty's own keys. OpenAI models go straight to OpenAI when
 * the instance has an OpenAI key; everything else goes through the AI Gateway.
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

export async function resolveTranscriptionModel(
  modelId: string,
  env: Environment = process.env,
): Promise<TranscriptionModelV4> {
  if (directOpenAI(modelId, env))
    return openAI(env).transcription(modelId.slice("openai/".length));
  return instanceGateway(env).transcriptionModel(modelId);
}
