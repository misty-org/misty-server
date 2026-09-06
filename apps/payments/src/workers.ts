import { z } from "zod";
import type { Logger } from "../../../packages/runtime/src/logger.js";
import { startPollingWorker } from "../../../packages/runtime/src/worker.js";

export const paymentsModeSchema = z.enum(["paused", "delivery", "active"]);
export type PaymentsMode = z.infer<typeof paymentsModeSchema>;
export function startPaymentsWorkers(mode: PaymentsMode, logger: Logger, operations: {
  webhooks: () => Promise<boolean>; reconciliation: () => Promise<boolean>;
  checkoutRecovery: () => Promise<boolean>; legacyCheckoutRecovery: () => Promise<boolean>;
  delivery: () => Promise<boolean>; accountClosure: () => Promise<boolean>;
}) {
  const workers: ReturnType<typeof startPollingWorker>[] = [];
  if (mode === "active") {
    workers.push(startPollingWorker({ name: "stripe-webhooks", runOnce: operations.webhooks, logger }),
      startPollingWorker({ name: "subscription-reconciliation", runOnce: operations.reconciliation, logger, idleMilliseconds: 10000 }),
      startPollingWorker({ name: "checkout-recovery", runOnce: operations.checkoutRecovery, logger, idleMilliseconds: 10000 }),
      startPollingWorker({ name: "legacy-checkout-recovery", runOnce: operations.legacyCheckoutRecovery, logger, idleMilliseconds: 10000 }),
      startPollingWorker({ name: "account-closure", runOnce: operations.accountClosure, logger, idleMilliseconds: 10000 }));
  }
  if (mode !== "paused") workers.push(startPollingWorker({ name: "entitlement-delivery", runOnce: operations.delivery, logger }));
  return workers;
}
