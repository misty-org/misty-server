import { pino } from "pino";
import type { RuntimeConfig } from "./config.js";

export function createLogger(config: RuntimeConfig) {
  return pino({
    level: config.logLevel,
    base: { service: config.service, version: config.version },
    redact: {
      paths: ["authorization", "cookie", "password", "token", "secret", "req.headers.authorization", "req.headers.cookie"],
      censor: "[REDACTED]",
    },
  });
}

export type Logger = ReturnType<typeof createLogger>;
