import { expect, it } from "vitest";
import { createEgressGuard, loadEgressBudget } from "./egress.js";
it("charges the full transfer against account and global ceilings and resets expired windows", () => {
  let time = 1000;
  const guard = createEgressGuard({ perIdentity: 100n, global: 150n }, () => time);
  guard.charge("one", 90);
  expect(() => guard.charge("one", 11)).toThrow(); guard.charge("one", 10);
  guard.charge("two", 50); expect(() => guard.charge("three", 1)).toThrow();
  time += 86400000; expect(() => guard.charge("one", 100)).not.toThrow();
});
it("retains bounded identity accounting without bypassing it when full", () => {
  const guard = createEgressGuard({ perIdentity: 100n, global: 1000n }, () => 1000, 1);
  guard.charge("one", 1); expect(() => guard.charge("two", 1)).toThrow();
  guard.charge("one", 99); expect(() => guard.charge("one", 1)).toThrow();
});
it("loads the existing byte-limit environment variables with Go-compatible defaults", () => {
  expect(loadEgressBudget({})).toEqual({ perIdentity: 25n << 30n, global: 200n << 30n });
  expect(loadEgressBudget({ MISTY_EGRESS_MAX_BYTES_PER_IDENTITY_DAY: "123", MISTY_EGRESS_MAX_BYTES_PER_DAY: "-1" })).toEqual({ perIdentity: 123n, global: 200n << 30n });
});
