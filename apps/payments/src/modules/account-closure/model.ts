import { z } from "zod";
import type { BillingAccount } from "../subscriptions/repository.js";

export class ClosureCleanupError extends Error { constructor(readonly code: "closure_history_unavailable" | "closure_identity_mismatch" | "closure_recovery_required" | "closure_pagination_invalid" | "closure_scan_limit" | "closure_provider_unavailable" | "closure_recovery_link_enabled") { super(code); } }
export interface ClosureAccount extends BillingAccount { attempts: number; closure_license_id: string }
export interface ClosureResource { kind: "checkout" | "subscription" | "customer"; resource_id: string; customer_id: string | null; phase: "sessions" | "subscriptions" | "delete"; cursor: string | null; pages: number }
const reference = z.union([z.string().min(1), z.object({ id: z.string().min(1) })]).transform(value => typeof value === "string" ? value : value.id);
export const closureSessionSchema = z.object({
  id: z.string().startsWith("cs_"), customer: reference.nullable(), subscription: reference.nullable(),
  status: z.enum(["open", "complete", "expired"]), mode: z.enum(["subscription", "payment", "setup"]),
  client_reference_id: z.string().nullable(), metadata: z.record(z.string(), z.string()).nullable(),
  payment_intent: reference.nullable().optional(), expires_at: z.number().int().positive(),
  after_expiration: z.object({ recovery: z.object({ enabled: z.boolean() }).nullable().optional() }).nullable().optional(),
});
export type ClosureSession = z.infer<typeof closureSessionSchema>;
export const customerSchema = z.object({ id: z.string().startsWith("cus_"), deleted: z.boolean().optional(), metadata: z.record(z.string(), z.string()).optional() });
export const resourcePageSchema = z.object({ data: z.array(z.object({ id: z.string().min(1), customer: reference.nullable() })).max(100), has_more: z.boolean() });
export interface ClosureGateway {
  retrieveSession: (id: string) => Promise<unknown>;
  expireSession: (id: string) => Promise<unknown>;
  retrieveSubscription: (id: string) => Promise<unknown>;
  cancelSubscription: (id: string) => Promise<unknown>;
  retrieveCustomer: (id: string) => Promise<unknown>;
  deleteCustomer: (id: string) => Promise<unknown>;
  listSessions: (query: { customer: string; limit: 100; starting_after?: string }) => Promise<unknown>;
  listSubscriptions: (query: { customer: string; status: "all"; limit: 100; starting_after?: string }) => Promise<unknown>;
}
