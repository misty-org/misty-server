import { expect, it } from "vitest";
import { parseWebhookPath } from "./config.js";

it("retains configured webhook URLs and static paths", () => {
  expect(parseWebhookPath(undefined)).toBe("/stripe/webhook");
  expect(parseWebhookPath(" https://payments.example.invalid/custom/webhook ")).toBe("/custom/webhook");
  expect(parseWebhookPath("http://localhost:8083/stripe/webhook")).toBe("/stripe/webhook");
});
it.each(["http://payments.example.invalid/webhook", "/readyz", "/internal/mutation", "/stripe/*", "/stripe/:path", "/stripe/webhook?test=1", "//elsewhere.invalid/webhook"])("rejects unsafe webhook configuration %s", (path) => {
  expect(() => parseWebhookPath(path)).toThrow();
});
