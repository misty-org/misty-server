import type { Environment, RuntimeConfig } from "../../../../../packages/runtime/src/config.js";

export function storageBackend(env: Environment): "filesystem" | "s3" {
  const value = env.MISTY_LIBRARY_BACKEND?.trim().toLowerCase() || (localDirectory(env) ? "filesystem" : "s3");
  if (value !== "filesystem" && value !== "s3") throw new Error("Unsupported library storage backend");
  return value;
}
function localDirectory(env: Environment) {
  return env.MISTY_LIBRARY_FILESYSTEM_DIR?.trim() || env.MISTY_LIBRARY_LOCAL_DIR?.trim() || "";
}
export function loadFilesystemConfig(env: Environment, config: Pick<RuntimeConfig, "environment" | "deployment">): string | null {
  if (storageBackend(env) !== "filesystem") return null;
  if (config.environment === "production" && config.deployment !== "self_hosted") throw new Error("Filesystem storage in production requires a self-hosted deployment");
  const directory = localDirectory(env);
  if (!directory || directory.includes("\0")) throw new Error("Filesystem storage requires a valid directory");
  return directory;
}
