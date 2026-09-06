import { expect, it, vi } from "vitest";
import { createExportFile } from "./export-file.js";

it("streams an exact multi-chunk UTF-8 manifest and releases its private descriptor once", async () => {
  const onClose = vi.fn(), signal = new AbortController().signal, file = await createExportFile({ onClose, signal });
  const text = JSON.stringify({ text: "📚ab".repeat(30000) });
  await file.write(text.slice(0, 100)); await file.write(text.slice(100));
  const response = file.response(); expect(response.headers.get("Content-Length")).toBe(String(Buffer.byteLength(text)));
  expect(await response.text()).toBe(text); expect(onClose).toHaveBeenCalledTimes(1); await file.close();
  await expect(file.write("late")).rejects.toThrow(); expect(() => file.response()).toThrow();
});
it("releases unfinished and abandoned files on cancellation without retaining admission", async () => {
  const controller = new AbortController(), onClose = vi.fn();
  const file = await createExportFile({ onClose, signal: controller.signal }); await file.write("before abort"); controller.abort(); await file.close();
  expect(onClose).toHaveBeenCalledTimes(1); expect(() => file.response()).toThrow();
  const other = await createExportFile({ onClose, signal: new AbortController().signal }); await other.write("x".repeat(200000));
  await other.response().body!.cancel(); expect(onClose).toHaveBeenCalledTimes(2);
});
it("rejects oversized output and expires a stalled response", async () => {
  const onClose = vi.fn(), file = await createExportFile({ onClose, signal: new AbortController().signal, maxBytes: 10 });
  try { await expect(file.write("x".repeat(11))).rejects.toMatchObject({ code: "account_export_too_large" }); } finally { await file.close(); }
  const stalled = await createExportFile({ onClose, signal: new AbortController().signal, readTimeoutMs: 20 });
  await stalled.write("x".repeat(200000)); const response = stalled.response();
  await vi.waitFor(() => expect(onClose).toHaveBeenCalledTimes(2)); await expect(response.text()).rejects.toThrow();
});
