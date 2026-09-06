import { z } from "zod";

const runtimeSchema = z.object({
  service: z.enum(["api", "payments"]),
  environment: z.enum(["development", "test", "production"]),
  deployment: z.enum(["hosted", "self_hosted"]),
  host: z.string().min(1),
  port: z.coerce.number().int().min(1).max(65535),
  logLevel: z.enum(["fatal", "error", "warn", "info", "debug", "trace", "silent"]),
  version: z.string().min(1),
});

export type RuntimeConfig = Readonly<z.infer<typeof runtimeSchema>>;
export type Environment = Readonly<Record<string, string | undefined>>;

/** The composition root is the only layer allowed to read process.env. */
export function loadRuntimeConfig(service: RuntimeConfig["service"], env: Environment): RuntimeConfig {
  return Object.freeze(runtimeSchema.parse({
    service,
    environment: env.MISTY_ENVIRONMENT ?? env.NODE_ENV ?? "development",
    deployment: env.MISTY_DEPLOYMENT_MODE ?? "hosted",
    host: env.HOST ?? "127.0.0.1",
    port: env.PORT ?? (service === "api" ? 8082 : 8083),
    logLevel: env.LOG_LEVEL ?? "info",
    version: env.MISTY_VERSION ?? "development",
  }));
}
