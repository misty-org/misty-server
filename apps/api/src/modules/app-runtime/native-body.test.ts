import { expect, it } from "vitest";
import { nativeBody, withNativeBody } from "./native-body.js";

it("shares the original validated body only with its exact internal request and method", async () => {
  const body = { text: "fixture", attachments: [] }, request = new Request("http://native-rpc.invalid/mail/drafts", { method: "POST" });
  expect(nativeBody(request, "mail.drafts.create")).toBeUndefined();
  await withNativeBody(request, "mail.drafts.create", body, async () => {
    expect(nativeBody(request, "mail.drafts.create")).toBe(body);
    expect(nativeBody(request, "mail.drafts.update")).toBeUndefined();
    expect(nativeBody(request.clone(), "mail.drafts.create")).toBeUndefined();
    expect(await request.text()).toBe("");
  });
  expect(nativeBody(request, "mail.drafts.create")).toBeUndefined();
});

it("removes validated bodies after failure and ignores forged client headers", async () => {
  const request = new Request("http://native-rpc.invalid/mail/drafts", { method: "POST", headers: { "X-Misty-Native-Body": "true", "X-Misty-Validated": "mail.drafts.create" }, body: "unvalidated" });
  expect(nativeBody(request, "mail.drafts.create")).toBeUndefined();
  await expect(withNativeBody(request, "mail.drafts.create", {}, () => { throw new Error("failed"); })).rejects.toThrow("failed");
  expect(nativeBody(request, "mail.drafts.create")).toBeUndefined();
});
