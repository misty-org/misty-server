import { randomBytes, randomUUID } from "node:crypto";
import type { PasswordHasher } from "../auth/passwords.js";
import { hashToken } from "../auth/service.js";
import { sessionTtlSeconds } from "../auth/model.js";
import type { SelfHostRepository } from "./repository.js";
import type { InstanceConfig } from "./config.js";

export type ProofVerifier = (token: string | undefined) => Promise<{ subject: string; expiresAt: Date }>;
export function createSelfHostService(options: { repository: SelfHostRepository; passwords: PasswordHasher; verify: ProofVerifier; config: InstanceConfig; now?: () => Date }) {
  const clock = options.now ?? (() => new Date());
  return {
    async instance() {
      const state = await options.repository.instance(options.config.name);
      return { ...state, bootstrap_required: options.config.deployment === "self_hosted" && state.bootstrap_required,
        deployment: options.config.deployment, protocol_version: 1, min_client_protocol: 1, max_client_protocol: 1,
        capabilities: options.config.capabilities, registration: options.config.deployment === "self_hosted" ? "invitation" : "open" };
    },
    access: options.repository.access,
    async createAccount(input: { name: string; username: string; email: string; password: string; credential: string; kind: "bootstrap" | "enroll"; proof?: string }) {
      const proof = await options.verify(input.proof), passwordHash = await options.passwords.hash(input.password);
      const token = randomBytes(32).toString("base64url");
      const user = await options.repository.createAccount({ name: input.name, username: input.username, email: input.email, passwordHash,
        credentialHash: hashToken(input.credential), kind: input.kind, subject: proof.subject, proofExpiresAt: proof.expiresAt,
        sessionHash: hashToken(token), sessionExpiresAt: new Date(clock().getTime() + sessionTtlSeconds * 1000) });
      return { user_id: user.id, name: user.name, username: user.username, email: user.email, token };
    },
    async invite(userId: string) {
      const id = `enrollment_${randomUUID()}`, invitation = randomBytes(32).toString("base64url"), expires_at = new Date(clock().getTime() + 7 * 86400_000);
      await options.repository.invite(userId, id, hashToken(invitation), expires_at);
      return { id, invitation, expires_at };
    },
    revoke: options.repository.revoke,
    async renew(userId: string, token: string | undefined) {
      const proof = await options.verify(token);
      await options.repository.renew(userId, proof.subject, proof.expiresAt);
      return { status: "eligible", expires_at: proof.expiresAt };
    },
  };
}
export type SelfHostService = ReturnType<typeof createSelfHostService>;
