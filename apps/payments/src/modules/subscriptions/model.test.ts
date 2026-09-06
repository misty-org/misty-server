import { describe, expect, it } from "vitest";
import { createPriceCatalog, nextReconciliation, normalizeSubscription } from "./model.js";

const catalog = createPriceCatalog([{ id: "price_pro", tier: "pro", interval: "month" }]);
const fixture = () => ({
  id: "sub_test", customer: "cus_test", metadata: { user_id: "user", license_id: "license", kind: "subscription", tier: "pro", interval: "month" },
  status: "active", cancel_at_period_end: false,
  items: { data: [{ current_period_end: 1900000000, price: { id: "price_pro", recurring: { interval: "month" } } }] },
});
describe("subscription normalization", () => {
  it("handles item-level billing periods and expanded customer IDs", () => {
    expect(normalizeSubscription({ ...fixture(), customer: { id: "cus_test" } }, catalog)).toMatchObject({
      customerId: "cus_test", projection: { tier: "pro", currentPeriodEnd: new Date(1900000000000).toISOString() },
    });
  });
  it("rejects metadata-price mismatches, unknown prices, extra items and missing paid periods", () => {
    expect(() => normalizeSubscription({ ...fixture(), metadata: { ...fixture().metadata, tier: "max" } }, catalog)).toThrow("price_invalid");
    expect(() => normalizeSubscription(fixture(), createPriceCatalog([]))).toThrow("price_invalid");
    expect(() => normalizeSubscription({ ...fixture(), items: { data: [...fixture().items.data, ...fixture().items.data] } }, catalog)).toThrow("subscription_invalid");
    const invalid = fixture();
    invalid.items.data[0]!.current_period_end = 0;
    expect(() => normalizeSubscription(invalid, catalog)).toThrow("subscription_invalid");
  });
  it("rejects ambiguous price configuration", () => {
    expect(() => createPriceCatalog([{ id: "price_pro", tier: "pro", interval: "month" }, { id: "price_pro", tier: "max", interval: "year" }])).toThrow("ambiguous");
  });
  it("schedules reconciliation six-hourly or shortly after a billing period", () => {
    const now = new Date("2026-09-05T00:00:00Z");
    expect(nextReconciliation(now, null).toISOString()).toBe("2026-09-05T06:00:00.000Z");
    expect(nextReconciliation(now, "2026-09-05T01:00:00Z").toISOString()).toBe("2026-09-05T01:15:00.000Z");
    expect(nextReconciliation(now, "2026-09-04T00:00:00Z").toISOString()).toBe("2026-09-05T00:15:00.000Z");
  });
});
