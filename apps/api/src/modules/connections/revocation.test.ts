import { expect, it, vi } from "vitest";
import { createConnectionCipher } from "./credentials.js";
import { createConnectionRevoker } from "./revocation.js";

const cipher = createConnectionCipher(Buffer.alloc(32, 11));
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}
function credential(provider: string, accessToken = "fixture-only+/=%") {
  const encrypted = cipher.encrypt(provider, Buffer.from(JSON.stringify({ access_token: accessToken })));
  return { provider, credential_ciphertext: encrypted.ciphertext, credential_nonce: encrypted.nonce, key_version: encrypted.keyVersion };
}
it("uses a fixed Google endpoint, form encoding, no redirects/retries, and cancels unused response bodies", async () => {
  const cancel = vi.fn(), response = () => new Response(new ReadableStream({ cancel }));
  const fetcher = vi.fn<typeof fetch>(async () => response()), signal = new AbortController().signal;
  const revoke = createConnectionRevoker(cipher, fetcher);
  expect(await revoke(credential("google"), [], signal)).toBe("provider_revoked");
  const [url, request] = fetcher.mock.calls[0]!;
  expect(url).toBe("https://oauth2.googleapis.com/revoke"); expect(request).toMatchObject({ method: "POST", redirect: "error", signal });
  expect(new URLSearchParams(String(request!.body)).get("token")).toBe("fixture-only+/=%"); expect(cancel).toHaveBeenCalledTimes(1);
  fetcher.mockResolvedValue(new Response(null, { status: 503 })); expect(await revoke(credential("google"), [], signal)).toBe("provider_revocation_failed_local_credentials_erased");
  expect(fetcher).toHaveBeenCalledTimes(2);
});
it("does not send malformed credentials or call unsupported revocation providers", async () => {
  const fetcher = vi.fn<typeof fetch>(), revoke = createConnectionRevoker(cipher, fetcher), signal = new AbortController().signal;
  expect(await revoke(credential("microsoft"), [], signal)).toBe("provider_session_not_revocable_local_credentials_erased");
  expect(await revoke(credential("google", "bad\r\nheader"), [], signal)).toBe("local_credentials_erased");
  expect(await revoke({ ...credential("google"), credential_nonce: Buffer.alloc(1) }, [], signal)).toBe("local_credentials_erased");
  expect(fetcher).not.toHaveBeenCalled();
});
it("bounds Figma cleanup to four concurrent requests and stops scheduling when the shared signal aborts", async () => {
  const controller = new AbortController(), allStarted = deferred(), released = deferred();
  let active = 0, peak = 0;
  const fetcher = vi.fn<typeof fetch>(async () => { active++; peak = Math.max(peak, active); if (active === 4) allStarted.resolve(); await released.promise; active--; return new Response(null, { status: 204 }); });
  const pending = createConnectionRevoker(cipher, fetcher)(credential("figma"), ["opaque/plus+%", "2", "3", "4", "5", "6"], controller.signal);
  await allStarted.promise; controller.abort(); released.resolve();
  expect(await pending).toBe("provider_session_not_revocable_local_credentials_erased"); expect(peak).toBe(4); expect(fetcher).toHaveBeenCalledTimes(4);
  expect(fetcher.mock.calls[0]![0]).toBe("https://api.figma.com/v2/webhooks/opaque%2Fplus%2B%25");
});
