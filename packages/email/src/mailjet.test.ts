import { expect, it, vi } from "vitest";
import { createMailjetSender, loadMailjetConfig } from "./mailjet.js";

const config = loadMailjetConfig({ MAILJET_API_KEY: "test-key", MAILJET_SECRET_KEY: "test-secret", MAILJET_FROM_EMAIL: "from@example.invalid" })!;
const message = { to: "to@example.invalid", subject: "Recovery", text: "Test recovery link", html: "<p>Test</p>" };
it("uses the Mailjet v3.1 contract with bounded calls and no redirect following", async () => {
  const request = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ Messages: [{ Status: "success" }] })));
  await createMailjetSender(config, request)(message);
  const [url, init] = request.mock.calls[0]!;
  expect(url).toBe("https://api.mailjet.com/v3.1/send");
  expect(init?.redirect).toBe("error");
  expect(init?.signal).toBeInstanceOf(AbortSignal);
  expect(init?.headers).toMatchObject({ Authorization: `Basic ${Buffer.from("test-key:test-secret").toString("base64")}` });
  expect(JSON.parse(init?.body as string)).toEqual({ Messages: [{ From: { Email: "from@example.invalid", Name: "" }, To: [{ Email: message.to }], Subject: message.subject, TextPart: message.text, HTMLPart: message.html }] });
});
it("rejects provider errors and oversized replies without retaining sensitive payloads", async () => {
  for (const response of [new Response("secret-reset-link", { status: 429 }), new Response('{"Messages":[{"Status":"error"}]}'), new Response("secret-reset-link"), new Response("x".repeat(16385))]) {
    await expect(createMailjetSender(config, vi.fn<typeof fetch>().mockResolvedValue(response))(message)).rejects.toMatchObject({ message: "Email delivery failed", reason: "response" });
  }
  await expect(createMailjetSender(config, vi.fn<typeof fetch>().mockRejectedValue(new Error("secret-reset-link")))(message)).rejects.toMatchObject({ message: "Email delivery failed", reason: "transport" });
});
it("rejects partial or unsafe configuration and bounds concurrent provider work", async () => {
  expect(loadMailjetConfig({})).toBeNull();
  expect(() => loadMailjetConfig({ MAILJET_API_KEY: "key" })).toThrow();
  for (const url of ["http://api.mailjet.com", "https://key:secret@api.mailjet.com", "https://api.mailjet.com/?key=secret"]) {
    expect(() => loadMailjetConfig({ MAILJET_API_KEY: "key", MAILJET_SECRET_KEY: "secret", MAILJET_FROM_EMAIL: "from@example.invalid", MAILJET_API_BASE_URL: url })).toThrow();
  }
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const sender = createMailjetSender(config, async () => { await gate; return new Response('{"Messages":[{"Status":"success"}]}'); });
  const pending = Array.from({ length: 16 }, () => sender(message));
  await expect(sender(message)).rejects.toMatchObject({ reason: "busy" });
  release(); await Promise.all(pending);
  await sender(message);
});
