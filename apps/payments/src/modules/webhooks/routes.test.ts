import Stripe from "stripe";
import { describe, expect, it, vi } from "vitest";
import { createWebhookRoutes } from "./routes.js";

const stripe = new Stripe("sk_test_local_fixture_only");
const secret = "whsec_local_fixture_only_123456789";
const payload = JSON.stringify({ id: "evt_fixture", object: "event", type: "customer.subscription.updated", created: 1788550000, data: { object: { id: "sub_fixture" } } });
function fixture() {
  const accept = vi.fn(async () => {});
  const app = createWebhookRoutes({ stripe, signingSecret: secret, inbox: { accept } });
  const signature = stripe.webhooks.generateTestHeaderString({ payload, secret });
  return { app, accept, signature };
}
describe("Stripe webhook acceptance", () => {
  it("verifies the raw body and waits for durable acceptance", async () => {
    const { app, accept, signature } = fixture();
    const response = await app.request("/", { method: "POST", headers: { "Stripe-Signature": signature }, body: payload });
    expect(response.status).toBe(200);
    expect(accept).toHaveBeenCalledWith(expect.objectContaining({ id: "evt_fixture", payloadSha256: expect.stringMatching(/^[a-f0-9]{64}$/) }));
  });
  it("rejects modified payload bytes even when the JSON is equivalent", async () => {
    const { app, accept, signature } = fixture();
    const response = await app.request("/", { method: "POST", headers: { "Stripe-Signature": signature }, body: payload + " " });
    expect(response.status).toBe(400);
    expect(accept).not.toHaveBeenCalled();
  });
  it("returns a retryable failure if persistence fails", async () => {
    const { app, accept, signature } = fixture();
    accept.mockRejectedValueOnce(new Error("private database details"));
    const response = await app.request("/", { method: "POST", headers: { "Stripe-Signature": signature }, body: payload });
    expect(response.status).toBe(500);
    expect(await response.text()).not.toContain("private database");
  });
  it("refuses an unconfigured verifier", () => {
    expect(() => createWebhookRoutes({ stripe, signingSecret: "", inbox: { accept: async () => {} } })).toThrow();
  });
});
