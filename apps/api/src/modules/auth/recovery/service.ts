import type { Logger } from "../../../../../../packages/runtime/src/logger.js";
import type { PasswordHasher } from "../passwords.js";
import { hashToken } from "../service.js";
import type { RecoveryConfig } from "./config.js";
import type { RecoveryRepository } from "./repository.js";
import type { RecoveryJobs } from "./jobs.js";
import { AuthBusy } from "../model.js";

export const resetTtlSeconds = 900;
export function createRecoveryService(options: { repository: RecoveryRepository; passwords: PasswordHasher; jobs: Pick<RecoveryJobs, "enqueue">; config: RecoveryConfig; logger: Logger; now?: () => Date }) {
  const clock = options.now ?? (() => new Date());
  return {
    redirectUrl: options.config.redirectUrl,
    async forgot(email: string) {
      // Every address follows the same enqueue path. Lookup and delivery happen
      // after the HTTP response, avoiding provider-dependent account timing.
      try { await options.jobs.enqueue(email); }
      catch (error) {
        options.logger.warn({ errorType: error instanceof Error ? error.name : "Unknown" }, "password recovery enqueue failed");
        throw new AuthBusy("Password recovery unavailable");
      }
    },
    async validate(token: string | undefined) { return Boolean(token?.trim()) && options.repository.validate(hashToken(token!.trim()), clock()); },
    async reset(token: string | undefined, password: string) {
      if (!token?.trim() || !await options.repository.validate(hashToken(token.trim()), clock())) return false;
      const passwordHash = await options.passwords.hash(password);
      return options.repository.reset(hashToken(token.trim()), passwordHash, clock());
    },
  };
}
export type RecoveryService = ReturnType<typeof createRecoveryService>;
