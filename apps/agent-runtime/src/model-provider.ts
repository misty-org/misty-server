import { createAnthropic } from "@ai-sdk/anthropic";
import { createGoogleGenerativeAI } from "@ai-sdk/google";
import { createOpenAI } from "@ai-sdk/openai";
import { createOpenAICompatible } from "@ai-sdk/openai-compatible";
import type { LanguageModelV4, LanguageModelV4CallOptions } from "@ai-sdk/provider";
import { createGateway } from "ai";

type Environment = Record<string, string | undefined>;
const directProviders = new Set(["openai", "anthropic", "google", "openai-compatible"]);

export function instanceModelConfig(env: Environment = process.env) {
  const provider = env.MISTY_AGENT_MODEL_PROVIDER?.trim() || "gateway";
  const model = env.MISTY_AGENT_MODEL?.trim() || "";
  const baseURL = env.MISTY_AGENT_MODEL_BASE_URL?.trim() || undefined;
  const apiKey = env.MISTY_AGENT_MODEL_API_KEY?.trim() || undefined;
  if (provider !== "gateway" && !directProviders.has(provider)) {
    throw new Error("Invalid MISTY_AGENT_MODEL_PROVIDER");
  }
  if (
    env.MISTY_DEPLOYMENT_MODE?.trim().toLowerCase() === "hosted" &&
    (provider !== "gateway" || model || baseURL || apiKey)
  ) {
    throw new Error("Instance model overrides require self_hosted deployment mode");
  }
  if (model && (!/^[^\s/]+\/\S+$/.test(model) || model.length > 200)) {
    throw new Error("MISTY_AGENT_MODEL must be a provider/model ID");
  }
  if (provider !== "gateway") {
    if (!model.startsWith(`${provider}/`)) {
      throw new Error("MISTY_AGENT_MODEL must use the configured provider prefix");
    }
    if (!apiKey && provider !== "openai-compatible") {
      throw new Error("MISTY_AGENT_MODEL_API_KEY is required for the configured provider");
    }
    if (provider === "openai-compatible" && !baseURL) {
      throw new Error("MISTY_AGENT_MODEL_BASE_URL is required for openai-compatible");
    }
  } else if (baseURL || apiKey) {
    throw new Error("Gateway credentials use AI_GATEWAY_API_KEY and AI_GATEWAY_BASE_URL");
  }
  if (baseURL) {
    let url: URL;
    try {
      url = new URL(baseURL);
    } catch {
      throw new Error("Invalid MISTY_AGENT_MODEL_BASE_URL");
    }
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    ) {
      throw new Error(
        "MISTY_AGENT_MODEL_BASE_URL must be an HTTP(S) URL without credentials, query, or fragment",
      );
    }
  }
  return { provider, model, baseURL, apiKey };
}

export function resolveInstanceModel(
  modelId: string,
  options: LanguageModelV4CallOptions,
  env: Environment = process.env,
): { model: LanguageModelV4; options: LanguageModelV4CallOptions } {
  const config = instanceModelConfig(env);
  if (config.provider === "gateway") {
    const gateway = createGateway({
      apiKey: env.AI_GATEWAY_API_KEY?.trim() || env.VERCEL_OIDC_TOKEN?.trim() || undefined,
      baseURL: env.AI_GATEWAY_BASE_URL?.trim() || undefined,
    });
    return { model: gateway(modelId), options };
  }
  if (modelId !== config.model)
    throw new Error("Requested model is not configured for this instance");
  const nativeId = modelId.slice(config.provider.length + 1);
  const settings = { apiKey: config.apiKey, baseURL: config.baseURL };
  let model: LanguageModelV4;
  switch (config.provider) {
    case "openai":
      model = createOpenAI(settings)(nativeId);
      break;
    case "anthropic":
      model = createAnthropic(settings)(nativeId);
      break;
    case "google":
      model = createGoogleGenerativeAI(settings)(nativeId);
      break;
    default:
      model = createOpenAICompatible({
        ...settings,
        name: "openai-compatible",
        baseURL: config.baseURL!,
      })(nativeId);
  }
  // A direct request never falls back to a different provider or Gateway bill.
  const { gateway: _gateway, ...providerOptions } = options.providerOptions ?? {};
  return {
    model,
    options: {
      ...options,
      providerOptions,
      reasoning: config.provider === "openai-compatible" ? undefined : options.reasoning,
    },
  };
}
