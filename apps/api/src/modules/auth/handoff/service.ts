import { randomBytes } from "node:crypto";
import { AuthRejected } from "../model.js";
import { hashToken } from "../service.js";
import { normalizeHandoffPath, type HandoffConfig } from "./config.js";
import type { HandoffRepository } from "./repository.js";

export const handoffSessionSeconds = 12 * 60 * 60;
export function createHandoffService(options: { repository: HandoffRepository; config: HandoffConfig; now?: () => Date }) {
  const clock = options.now ?? (() => new Date());
  return {
    async mint(sourceToken: string, redirectPath: string) {
      const path = normalizeHandoffPath(redirectPath);
      if (!path) throw new Error("Invalid normalized handoff path");
      const token = randomBytes(32).toString("base64url"), now = clock();
      if (!await options.repository.mint({ sourceSessionHash: hashToken(sourceToken), tokenHash: hashToken(token), redirectPath: path,
        now, expiresAt: new Date(now.getTime() + 60000) })) throw new AuthRejected("not authenticated");
      const url = new URL(options.config.startUrl);
      url.searchParams.set("token", token);
      return url.href;
    },
    async start(token: string | undefined) {
      const fallback = { location: options.config.websiteUrl + "/settings", sessionToken: null };
      if (!token?.trim()) return fallback;
      const sessionToken = randomBytes(32).toString("base64url"), now = clock();
      const path = await options.repository.redeem({ tokenHash: hashToken(token.trim()), sessionHash: hashToken(sessionToken), now,
        sessionExpiresAt: new Date(now.getTime() + handoffSessionSeconds * 1000) });
      return path === null ? fallback : { location: options.config.websiteUrl + (normalizeHandoffPath(path) ?? "/settings"), sessionToken };
    },
  };
}
export type HandoffService = ReturnType<typeof createHandoffService>;
