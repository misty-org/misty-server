import { createHash, randomBytes, randomUUID } from "node:crypto";
import type { AuthRepository } from "./repository.js";
import type { PasswordHasher } from "./passwords.js";
import { AuthRejected, SelfHostProofRequired, sessionTtlSeconds } from "./model.js";

export const hashToken = (token: string) => createHash("sha256").update(token).digest("hex");
export function createAuthService(options: { repository: AuthRepository; passwords: PasswordHasher; now?: () => Date;
  deployment: "hosted" | "self_hosted"; verifySelfHostProof?: (token: string | undefined) => Promise<{ subject: string; expiresAt: Date }> }) {
  const clock = options.now ?? (() => new Date());
  const newSession = () => ({ token: randomBytes(32).toString("base64url"), expiresAt: new Date(clock().getTime() + sessionTtlSeconds * 1000) });
  return {
    async register(input: { name: string; username: string; email: string; password: string; analyticsEnabled: boolean }) {
      if (options.deployment !== "hosted") throw new AuthRejected("self_host_registration_closed");
      const passwordHash = await options.passwords.hash(input.password);
      const session = newSession();
      const user = await options.repository.register({ id: randomUUID(), licenseId: randomUUID(), name: input.name, username: input.username,
        email: input.email, passwordHash, tokenHash: hashToken(session.token), expiresAt: session.expiresAt, analyticsEnabled: input.analyticsEnabled });
      return { user, token: session.token };
    },
    async login(input: { email: string; password: string; selfHostProof?: string }) {
      const user = await options.repository.findByEmail(input.email);
      if (!await options.passwords.verify(input.password, user?.password_hash ?? null) || !user) throw new AuthRejected("invalid credentials");
      let proof: { subject: string; expiresAt: Date } | undefined;
      if (options.deployment === "self_hosted") {
        if (!options.verifySelfHostProof) throw new SelfHostProofRequired("Self-host proof verifier is unavailable");
        proof = await options.verifySelfHostProof(input.selfHostProof);
      }
      const session = newSession();
      const accepted = await options.repository.createSession({ userId: user.id, expectedPasswordHash: user.password_hash,
        tokenHash: hashToken(session.token), expiresAt: session.expiresAt, ...(proof ? { selfHostProof: proof } : {}) });
      return { user: accepted, token: session.token };
    },
    async authenticate(token: string) { return options.repository.findSession(hashToken(token), clock()); },
    async logout(token: string | null) { if (token) await options.repository.deleteSession(hashToken(token)); },
  };
}
export type AuthService = ReturnType<typeof createAuthService>;
