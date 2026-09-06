import { expect, it } from "vitest";
import { loadAssetConfig } from "./asset-config.js";
it("preserves bounded upload limits and signed URL duration settings", () => {
  expect(loadAssetConfig({})).toEqual({ noteMaxBytes: 15728640, drawingMaxBytes: 15728640, uploadTtlMs: 900000, downloadTtlMs: 120000 });
  expect(loadAssetConfig({ MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES: "1024", MISTY_R2_DOWNLOAD_URL_TTL: "1m30s" })).toMatchObject({ noteMaxBytes: 1024, downloadTtlMs: 90000 });
  for (const value of ["0", "-1", "15728641", "1e3"]) expect(() => loadAssetConfig({ MISTY_DRAWING_ASSET_MAX_FILE_BYTES: value })).toThrow();
  for (const value of ["2h", "1s", "60", "Infinitys"]) expect(() => loadAssetConfig({ MISTY_R2_UPLOAD_URL_TTL: value })).toThrow();
});
