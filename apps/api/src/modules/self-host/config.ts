import type { Environment, RuntimeConfig } from "../../../../../packages/runtime/src/config.js";
import { storageBackend } from "../storage/filesystem-config.js";

export function loadInstanceConfig(env: Environment, deployment: RuntimeConfig["deployment"]) {
  const name = env.MISTY_INSTANCE_NAME?.trim() || (deployment === "self_hosted" ? "Misty Self-hosted" : "Misty Hosted");
  if ([...name].length > 120) throw new Error("MISTY_INSTANCE_NAME must be at most 120 characters");
  const hosted = deployment === "hosted";
  return { name, deployment, capabilities: { collaboration: true, library: true, notes: true, drawings: true,
    hosted_billing: hosted, hosted_integrations: hosted, hosted_ai: hosted, storage_backend: storageBackend(env) } };
}
export type InstanceConfig = ReturnType<typeof loadInstanceConfig>;
