import { readFile } from "node:fs/promises";
import { expect, it } from "vitest";
import { billingAiUsage } from "./usage-response.js";
const fixtures = JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/billing-ai-usage.json", import.meta.url), "utf8")) as {
  name: string; allowance: number; balance: number; reserved: number; expected: Record<string, unknown> & { reset_at: string };
}[];
it.each(fixtures)("matches Go's personal and Space usage response: $name", (fixture) => {
  const resetAt = new Date(fixture.expected.reset_at);
  expect(billingAiUsage({ allowance: BigInt(fixture.allowance), remaining: BigInt(fixture.balance), reserved: BigInt(fixture.reserved), consumed: 0n, resetAt }))
    .toEqual({ ...fixture.expected, reset_at: resetAt });
});
