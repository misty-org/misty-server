import { expect, it, vi } from "vitest";
import { pino } from "pino";
import { startPaymentsWorkers } from "./workers.js";

it.each(["paused", "delivery", "active"] as const)("starts only the owned jobs in %s mode and drains every started worker", async (mode) => {
  const operations = { webhooks: vi.fn(async () => false), reconciliation: vi.fn(async () => false),
    checkoutRecovery: vi.fn(async () => false), legacyCheckoutRecovery: vi.fn(async () => false), delivery: vi.fn(async () => false), accountClosure: vi.fn(async () => false) };
  const workers = startPaymentsWorkers(mode, pino({ level: "silent" }), operations);
  await Promise.all(workers.map((worker) => worker.stop()));
  for (const [name, operation] of Object.entries(operations)) {
    expect(operation).toHaveBeenCalledTimes(mode === "active" || (mode === "delivery" && name === "delivery") ? 1 : 0);
  }
});
