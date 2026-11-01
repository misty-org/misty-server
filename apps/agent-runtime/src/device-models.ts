import { z } from "zod";
import { ModelSetupError, parseModelId, providerIds, type ModelRoute } from "./model-provider.js";
import { searchProviderIds } from "./web-search/provider.js";

/**
 * The runtime on the person's desktop (docs/agents/runtime.md) works on that device's
 * model keys and choices, and on its web search key. The app sends them over the signed
 * local route `/v1/local/models` whenever they change; they live only in this process's
 * memory and reach only their provider.
 */
export const desktopRuntime = () => process.env.MISTY_RUNTIME_DESKTOP === "1";

const roles = ["agent", "fast", "embedding", "transcription", "speech"] as const;
export type DeviceRole = (typeof roles)[number];

const deviceModelsSchema = z.object({
  version: z.literal(1),
  providers: z.partialRecord(z.enum(providerIds), z.object({
    apiKey: z.string().max(8192).optional(),
    baseURL: z.string().max(2048).optional(),
  })),
  models: z.partialRecord(z.enum(roles), z.string().min(3).max(200)),
  reasoning: z.enum(["none", "minimal", "low", "medium", "high", "xhigh"]).optional(),
  /** The search provider and key behind web.search (src/web-search/). */
  search: z.object({ provider: z.enum(searchProviderIds), apiKey: z.string().min(1).max(8192) }).optional(),
});
export type DeviceModels = z.infer<typeof deviceModelsSchema>;

// Workflow bundles its steps apart from the routes, each with its own copy of this module, so
// the models live on the process rather than in a module variable.
const slot = Symbol.for("misty.deviceModels");
const store = globalThis as typeof globalThis & { [slot]?: DeviceModels };
const models = () => store[slot];

/** Replaces the device's models; an empty body clears them. False when the body isn't one. */
export function setDeviceModels(value: unknown): boolean {
  if (value === null || (typeof value === "object" && Object.keys(value as object).length === 0)) {
    delete store[slot];
    return true;
  }
  const parsed = deviceModelsSchema.safeParse(value);
  if (!parsed.success) return false;
  store[slot] = parsed.data;
  return true;
}

/** The device's model for a role. Vision work uses the agent model. */
export function deviceModelId(role: DeviceRole): string | undefined {
  return models()?.models[role] || (role === "fast" ? models()?.models.agent : undefined);
}

export const deviceReasoning = () => models()?.reasoning ?? "low";

/** The route a model runs on with this device's key for its provider. */
export function deviceRoute(modelId: string): ModelRoute {
  const { provider } = parseModelId(modelId);
  const saved = models()?.providers[provider];
  if (!saved) throw new ModelSetupError("model_key_needed", "Add a model key in Settings → Models.");
  return {
    provider: "device",
    reasoning: deviceReasoning(),
    credential: { provider, apiKey: saved.apiKey ?? "", baseURL: saved.baseURL },
  };
}

/** The device's web search provider and key; without one, web.search isn't offered. */
export const deviceSearch = () => models()?.search;

/** Every key the device gave, so a failure never repeats one. */
export function deviceSecrets(): string[] {
  return [...Object.values(models()?.providers ?? {}).flatMap((saved) => saved?.apiKey || []), ...(models()?.search ? [models()!.search!.apiKey] : [])];
}
